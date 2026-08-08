package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// ReservationRequest commits stock to a sales order or a remelt order without
// moving it yet (requirement sections 9 and 26).
//
// Reserved stock stays on hand and still counts against capacity; what it
// changes is Available Stock, which is what a planner may commit next.
type ReservationRequest struct {
	FactoryID     int64  `json:"factoryId"`
	StorageCode   string `json:"storageCode"`
	ProductCode   string `json:"productCode"`
	PackagingCode string `json:"packagingCode"`
	BatchNo       string `json:"batchNo"`
	// Supply either axis; the other is derived from the packaging master.
	PackageQuantity *float64 `json:"packageQuantity,omitempty"`
	WeightQuantity  *float64 `json:"weightQuantity,omitempty"`
	ReferenceDoc    string   `json:"referenceDoc"`
	UpdatedBy       string   `json:"updatedBy"`
}

// ReservationResult reports the position after reserving or releasing.
type ReservationResult struct {
	StorageCode   string            `json:"storageCode"`
	ProductCode   string            `json:"productCode"`
	PackagingCode string            `json:"packagingCode"`
	BatchNo       string            `json:"batchNo"`
	Applied       capacity.Quantity `json:"applied"`
	OnHand        capacity.Quantity `json:"onHand"`
	Reserved      capacity.Quantity `json:"reserved"`
	Available     capacity.Quantity `json:"available"`
}

// Reserve commits stock, refusing to reserve more than is available.
func (s *Service) Reserve(ctx context.Context, req ReservationRequest) (ReservationResult, error) {
	return s.adjustReservation(ctx, req, 1)
}

// Release frees previously reserved stock.
func (s *Service) Release(ctx context.Context, req ReservationRequest) (ReservationResult, error) {
	return s.adjustReservation(ctx, req, -1)
}

func (s *Service) adjustReservation(ctx context.Context, req ReservationRequest, sign float64) (ReservationResult, error) {
	req.StorageCode = strings.TrimSpace(req.StorageCode)

	if req.PackageQuantity == nil && req.WeightQuantity == nil {
		return ReservationResult{}, fmt.Errorf("%w: supply packageQuantity or weightQuantity", ErrValidation)
	}

	location, err := s.store.GetStorageLocation(ctx, req.FactoryID, req.StorageCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ReservationResult{}, fmt.Errorf("%w: unknown storage location %q", ErrValidation, req.StorageCode)
		}
		return ReservationResult{}, err
	}
	product, err := s.productByCode(ctx, req.ProductCode)
	if err != nil {
		return ReservationResult{}, err
	}
	packaging, err := s.packagingByCode(ctx, req.PackagingCode)
	if err != nil {
		return ReservationResult{}, err
	}

	// Derive whichever axis the caller left out, exactly as a movement does.
	var qty capacity.Quantity
	switch {
	case req.PackageQuantity != nil && req.WeightQuantity != nil:
		qty = capacity.Quantity{Packages: *req.PackageQuantity, Weight: *req.WeightQuantity}
	case req.PackageQuantity != nil:
		if packaging.IsBulk {
			return ReservationResult{}, fmt.Errorf("%w: %s is bulk packaging, supply weightQuantity",
				ErrValidation, packaging.Code)
		}
		qty = capacity.FromPackages(*req.PackageQuantity, packaging.WeightInTon)
	default:
		qty = capacity.FromWeight(*req.WeightQuantity, packaging.WeightInTon)
	}
	if qty.Weight < 0 || qty.Packages < 0 {
		return ReservationResult{}, fmt.Errorf("%w: quantities must be positive magnitudes", ErrValidation)
	}

	applied, balance, err := s.store.AdjustReservation(ctx, postgres.ReservationAdjustment{
		StorageLocationID: location.ID,
		ProductID:         product.ID,
		PackagingTypeID:   packaging.ID,
		BatchNo:           req.BatchNo,
		DeltaPackages:     sign * qty.Packages,
		DeltaWeight:       sign * qty.Weight,
		UpdatedBy:         auth.Actor(ctx, req.UpdatedBy),
	})
	if err != nil {
		if errors.Is(err, postgres.ErrInsufficientStock) {
			return ReservationResult{}, fmt.Errorf("%w: %s", ErrValidation, err.Error())
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ReservationResult{}, fmt.Errorf(
				"%w: %s holds no %s in %s packaging for batch %q",
				ErrNotFound, req.StorageCode, product.Name, packaging.Code, req.BatchNo)
		}
		return ReservationResult{}, err
	}

	return ReservationResult{
		StorageCode:   location.StorageCode,
		ProductCode:   product.Code,
		PackagingCode: packaging.Code,
		BatchNo:       req.BatchNo,
		Applied:       applied,
		OnHand: capacity.Quantity{
			Packages: capacity.Round(balance.PackageQty, 3),
			Weight:   capacity.Round(balance.WeightQty, 3),
		},
		Reserved: capacity.Quantity{
			Packages: capacity.Round(balance.ReservedPackage, 3),
			Weight:   capacity.Round(balance.ReservedWeight, 3),
		},
		Available: capacity.Quantity{
			Packages: capacity.Round(balance.AvailablePackages(), 3),
			Weight:   capacity.Round(balance.AvailableWeight(), 3),
		},
	}, nil
}
