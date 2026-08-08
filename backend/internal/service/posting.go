package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// MovementRequest is a proposed inventory movement.
//
// Exactly one of PackageQuantity or WeightQuantity need be supplied: the other
// is derived from the packaging master (requirement section 14). Both are
// positive magnitudes; the movement type decides the sign.
type MovementRequest struct {
	FactoryID       int64     `json:"factoryId"`
	MovementDate    time.Time `json:"movementDate"`
	MovementType    string    `json:"movementType"`
	StorageCode     string    `json:"storageCode"`
	ProductCode     string    `json:"productCode"`
	PackagingCode   string    `json:"packagingCode"`
	BatchNo         string    `json:"batchNo"`
	PackageQuantity *float64  `json:"packageQuantity,omitempty"`
	WeightQuantity  *float64  `json:"weightQuantity,omitempty"`
	ReferenceDoc    string    `json:"referenceDoc"`
	Remark          string    `json:"remark"`
	// Override posts a movement that capacity validation blocked
	// (requirement section 24.5). It requires a reason.
	Override       bool   `json:"override"`
	OverrideReason string `json:"overrideReason"`
	PostedBy       string `json:"postedBy"`
}

// MovementResponse reports what validation found and, when posted, the id of
// the ledger line.
type MovementResponse struct {
	Posted     bool                      `json:"posted"`
	MovementID int64                     `json:"movementId,omitempty"`
	Direction  string                    `json:"direction"`
	Quantity   capacity.Quantity         `json:"quantity"`
	Validation capacity.ValidationResult `json:"validation"`
}

// ErrValidation means the caller supplied something the system cannot act on,
// as opposed to a capacity rule firing.
var ErrValidation = errors.New("invalid request")

// ErrNotFound means the requested record does not exist. It is a normal
// outcome, not a server fault: a storage location with no daily plan simply
// cannot be projected.
var ErrNotFound = errors.New("not found")

// ErrConflict means the caller's view of a record is stale — somebody else
// changed it first.
var ErrConflict = errors.New("conflict")

// ValidateMovement runs the pre-posting capacity checks without writing
// anything (requirement section 24).
func (s *Service) ValidateMovement(ctx context.Context, req MovementRequest) (MovementResponse, error) {
	_, resp, err := s.prepare(ctx, req)
	return resp, err
}

// PostMovement validates and, if permitted, writes the movement and updates
// the balance.
func (s *Service) PostMovement(ctx context.Context, req MovementRequest) (MovementResponse, error) {
	prepared, resp, err := s.prepare(ctx, req)
	if err != nil {
		return resp, err
	}

	if !resp.Validation.Allowed {
		if !req.Override {
			return resp, nil
		}
		if req.OverrideReason == "" {
			return resp, fmt.Errorf("%w: an override requires a reason", ErrValidation)
		}
	}

	id, err := s.store.PostMovement(ctx, prepared)
	if err != nil {
		return resp, err
	}
	resp.Posted = true
	resp.MovementID = id
	return resp, nil
}

// prepare resolves master data, derives the second quantity axis and runs the
// capacity rules. It returns the store-ready movement alongside the response.
func (s *Service) prepare(ctx context.Context, req MovementRequest) (postgres.NewMovement, MovementResponse, error) {
	var out postgres.NewMovement
	var resp MovementResponse

	if req.MovementDate.IsZero() {
		req.MovementDate = time.Now()
	}
	if req.PackageQuantity == nil && req.WeightQuantity == nil {
		return out, resp, fmt.Errorf("%w: supply packageQuantity or weightQuantity", ErrValidation)
	}

	movementType, err := s.store.GetMovementType(ctx, req.MovementType)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, resp, fmt.Errorf("%w: unknown movement type %q", ErrValidation, req.MovementType)
		}
		return out, resp, err
	}

	loc, err := s.store.GetStorageLocation(ctx, req.FactoryID, req.StorageCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, resp, fmt.Errorf("%w: unknown storage location %q", ErrValidation, req.StorageCode)
		}
		return out, resp, err
	}

	products, err := s.store.ListProducts(ctx)
	if err != nil {
		return out, resp, err
	}
	var product domain.Product
	for _, p := range products {
		if p.Code == req.ProductCode {
			product = p
			break
		}
	}
	if product.ID == 0 {
		return out, resp, fmt.Errorf("%w: unknown product %q", ErrValidation, req.ProductCode)
	}

	packagings, err := s.store.ListPackagingTypes(ctx)
	if err != nil {
		return out, resp, err
	}
	var packaging domain.PackagingType
	for _, k := range packagings {
		if k.Code == req.PackagingCode {
			packaging = k
			break
		}
	}
	if packaging.ID == 0 {
		return out, resp, fmt.Errorf("%w: unknown packaging type %q", ErrValidation, req.PackagingCode)
	}

	// Derive whichever axis the caller left out.
	var qty capacity.Quantity
	switch {
	case req.PackageQuantity != nil && req.WeightQuantity != nil:
		qty = capacity.Quantity{Packages: *req.PackageQuantity, Weight: *req.WeightQuantity}
	case req.PackageQuantity != nil:
		if packaging.IsBulk {
			return out, resp, fmt.Errorf("%w: %s is bulk packaging, supply weightQuantity",
				ErrValidation, packaging.Code)
		}
		qty = capacity.FromPackages(*req.PackageQuantity, packaging.WeightInTon)
	default:
		qty = capacity.FromWeight(*req.WeightQuantity, packaging.WeightInTon)
	}
	if qty.Weight < 0 || qty.Packages < 0 {
		return out, resp, fmt.Errorf("%w: quantities must be positive magnitudes", ErrValidation)
	}
	resp.Quantity = qty
	resp.Direction = movementType.Direction

	// Capacity rules only apply to movements that add stock.
	checkQty := qty
	if movementType.Direction == domain.DirectionOut {
		checkQty = capacity.Quantity{}
	}

	bands, err := s.store.ListThresholdBands(ctx, req.FactoryID)
	if err != nil {
		return out, resp, err
	}
	totals, err := s.store.LocationTotal(ctx, loc.ID)
	if err != nil {
		return out, resp, err
	}
	curPackages, curWeight, err := s.store.ProductStock(ctx, loc.ID, product.ID, packaging.ID)
	if err != nil {
		return out, resp, err
	}
	caps, err := s.store.ListStorageProductCapacity(ctx, postgres.CapacityFilter{
		StorageLocationID: loc.ID,
		ProductID:         product.ID,
		PackagingTypeID:   packaging.ID,
		OnDate:            req.MovementDate,
	})
	if err != nil {
		return out, resp, err
	}
	var productCap *domain.StorageProductCapacity
	if len(caps) > 0 {
		productCap = &caps[0]
	}

	if movementType.RequiresCapacityCheck {
		resp.Validation = capacity.Validate(
			capacity.LocationState{
				Location:         loc,
				TotalWeight:      totals.TotalWeight,
				ProductIDsOnHand: totals.ProductIDs,
			},
			capacity.ProductState{
				Capacity: productCap,
				Current:  capacity.Quantity{Packages: curPackages, Weight: curWeight},
			},
			capacity.Receipt{
				ProductID:       product.ID,
				ProductName:     product.Name,
				PackagingTypeID: packaging.ID,
				PackagingCode:   packaging.Code,
				Quantity:        checkQty,
			},
			bands,
		)
	} else {
		resp.Validation = capacity.ValidationResult{Allowed: true}
		// An issue cannot take out more than is there.
		if movementType.Direction == domain.DirectionOut && qty.Weight > curWeight+capacity.Epsilon {
			resp.Validation = capacity.ValidationResult{
				Allowed:          false,
				RequiresOverride: true,
				Findings: []capacity.Finding{{
					Check:    capacity.CheckMinimumStock,
					Severity: capacity.FindingBlock,
					Message: fmt.Sprintf("%s holds %.3f %s of %s in %s, less than the %.3f requested",
						loc.StorageName, curWeight, loc.CapacityUOM, product.Name, packaging.Code, qty.Weight),
					Limit:     capacity.Round(curWeight, 3),
					Projected: capacity.Round(qty.Weight, 3),
					Excess:    capacity.Round(qty.Weight-curWeight, 3),
					UOM:       loc.CapacityUOM,
				}},
			}
		}
	}

	uomID, err := s.store.UOMID(ctx, "TON")
	if err != nil {
		return out, resp, err
	}

	// The ledger stores signed quantities.
	sign := 1.0
	if movementType.Direction == domain.DirectionOut {
		sign = -1
	}

	out = postgres.NewMovement{
		FactoryID:              req.FactoryID,
		MovementDate:           req.MovementDate,
		MovementTypeID:         movementType.ID,
		StorageLocationID:      loc.ID,
		ProductID:              product.ID,
		PackagingTypeID:        packaging.ID,
		BatchNo:                req.BatchNo,
		PackageQty:             sign * qty.Packages,
		WeightQty:              sign * qty.Weight,
		WeightUOMID:            uomID,
		ReferenceDoc:           req.ReferenceDoc,
		Remark:                 req.Remark,
		CapacityOverride:       req.Override && !resp.Validation.Allowed,
		CapacityOverrideReason: req.OverrideReason,
		CreatedBy:              req.PostedBy,
	}
	if !out.CapacityOverride {
		out.CapacityOverrideReason = ""
	}
	return out, resp, nil
}
