package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/auth"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// SaveResult reports what a master data write did.
type SaveResult struct {
	ID       int64     `json:"id"`
	Created  bool      `json:"created"`
	Warnings []Warning `json:"warnings,omitempty"`
}

// SaveStorageLocation creates or updates a tank, silo or warehouse.
//
// Beyond the input's own rules, this checks what only the database knows: that
// the storage type and unit exist, and that a capacity is not being cut below
// the stock already sitting in the building.
func (s *Service) SaveStorageLocation(ctx context.Context, in StorageLocationInput) (SaveResult, error) {
	if err := in.Validate(); err != nil {
		return SaveResult{}, err
	}

	types, err := s.store.ListStorageTypes(ctx)
	if err != nil {
		return SaveResult{}, err
	}
	var storageTypeID int64
	for _, t := range types {
		if t.Code == in.StorageTypeCode {
			storageTypeID = t.ID
			break
		}
	}
	if storageTypeID == 0 {
		return SaveResult{}, fmt.Errorf("%w: unknown storage type %q", ErrValidation, in.StorageTypeCode)
	}

	uomID, err := s.store.UOMID(ctx, in.CapacityUOM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SaveResult{}, fmt.Errorf("%w: unknown unit of measure %q", ErrValidation, in.CapacityUOM)
		}
		return SaveResult{}, err
	}

	var warnings []Warning

	// Shrinking a warehouse below what is in it would leave the system
	// reporting over 100% with no way to post anything out of the excess.
	if existing, err := s.store.GetStorageLocation(ctx, in.FactoryID, in.StorageCode); err == nil {
		totals, err := s.store.LocationTotal(ctx, existing.ID)
		if err != nil {
			return SaveResult{}, err
		}
		if totals.TotalWeight > in.PhysicalCapacity+capacity.Epsilon {
			if !in.Force {
				return SaveResult{}, fmt.Errorf(
					"%w: %s currently holds %.3f %s, which is more than the new capacity of %.3f. "+
						"Move the stock first, or set force to save anyway",
					ErrValidation, in.StorageCode, totals.TotalWeight, in.CapacityUOM, in.PhysicalCapacity)
			}
			warnings = append(warnings, Warning{
				Field: "physicalCapacity",
				Message: fmt.Sprintf("%s holds %.3f %s, above the new capacity of %.3f",
					in.StorageCode, totals.TotalWeight, in.CapacityUOM, in.PhysicalCapacity),
			})
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return SaveResult{}, err
	}

	id, err := s.store.SaveStorageLocation(ctx, postgres.UpsertStorageLocation{
		FactoryID:              in.FactoryID,
		StorageCode:            in.StorageCode,
		StorageName:            in.StorageName,
		StorageTypeID:          storageTypeID,
		PhysicalCapacity:       in.PhysicalCapacity,
		CapacityUOMID:          uomID,
		MinimumStockLevel:      in.MinimumStockLevel,
		SafeCapacityPercentage: in.SafeCapacityPercentage,
		AllowMixedProducts:     in.AllowMixedProducts,
		AllowMixedBatches:      in.AllowMixedBatches,
		Status:                 in.Status,
		EffectiveFrom:          in.EffectiveFrom,
		EffectiveTo:            in.EffectiveTo,
		Remark:                 in.Remark,
		Version:                in.Version,
		UpdatedBy:              auth.Actor(ctx, in.UpdatedBy),
	})
	if err != nil {
		return SaveResult{}, translateWriteError(err)
	}

	return SaveResult{ID: id, Created: in.Version == nil, Warnings: warnings}, nil
}

// SaveStorageProductCapacity configures one cell of the
// warehouse + product + packaging matrix.
func (s *Service) SaveStorageProductCapacity(ctx context.Context, in StorageProductCapacityInput) (SaveResult, error) {
	if err := in.Validate(); err != nil {
		return SaveResult{}, err
	}

	locations, err := s.store.ListStorageLocations(ctx, postgres.StorageLocationFilter{
		StorageCode: in.StorageCode, IncludeInactive: true,
	})
	if err != nil {
		return SaveResult{}, err
	}
	if len(locations) == 0 {
		return SaveResult{}, fmt.Errorf("%w: unknown storage location %q", ErrValidation, in.StorageCode)
	}
	location := locations[0]

	product, err := s.productByCode(ctx, in.ProductCode)
	if err != nil {
		return SaveResult{}, err
	}
	packaging, err := s.packagingByCode(ctx, in.PackagingCode)
	if err != nil {
		return SaveResult{}, err
	}

	uomID, err := s.store.UOMID(ctx, in.WeightUOM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SaveResult{}, fmt.Errorf("%w: unknown unit of measure %q", ErrValidation, in.WeightUOM)
		}
		return SaveResult{}, err
	}

	var warnings []Warning

	// A product ceiling above the building's own capacity can never be
	// reached. It is not harmful, but it is almost always a mistake.
	if in.MaximumWeightQty != nil && *in.MaximumWeightQty > location.PhysicalCapacity+capacity.Epsilon {
		warnings = append(warnings, Warning{
			Field: "maximumWeightQuantity",
			Message: fmt.Sprintf("%.3f exceeds %s's physical capacity of %.3f, so it can never be reached",
				*in.MaximumWeightQty, location.StorageCode, location.PhysicalCapacity),
		})
	}
	// A package ceiling on storage that does not count packages is inert.
	if in.MaximumPackageQty != nil && !location.TracksPackages {
		warnings = append(warnings, Warning{
			Field: "maximumPackageQuantity",
			Message: fmt.Sprintf("%s is %s storage, which is managed by weight, so the package ceiling is not enforced",
				location.StorageCode, location.StorageTypeName),
		})
	}
	// Bulk packaging has no package count to limit.
	if in.MaximumPackageQty != nil && packaging.IsBulk {
		warnings = append(warnings, Warning{
			Field:   "maximumPackageQuantity",
			Message: fmt.Sprintf("%s is bulk packaging and has no package count", packaging.Code),
		})
	}

	id, err := s.store.SaveStorageProductCapacity(ctx, postgres.UpsertStorageProductCapacity{
		StorageLocationID: location.ID,
		ProductID:         product.ID,
		PackagingTypeID:   packaging.ID,
		MaximumPackageQty: in.MaximumPackageQty,
		MaximumWeightQty:  in.MaximumWeightQty,
		MinimumStockQty:   in.MinimumStockQty,
		MaximumSafeQty:    in.MaximumSafeQty,
		WeightUOMID:       uomID,
		EffectiveFrom:     in.EffectiveFrom,
		EffectiveTo:       in.EffectiveTo,
		Status:            in.Status,
		Remark:            in.Remark,
		Version:           in.Version,
		UpdatedBy:         auth.Actor(ctx, in.UpdatedBy),
	})
	if err != nil {
		return SaveResult{}, translateWriteError(err)
	}

	return SaveResult{ID: id, Created: in.Version == nil, Warnings: warnings}, nil
}

// SavePackagingType maintains the packaging master.
func (s *Service) SavePackagingType(ctx context.Context, in PackagingTypeInput) (SaveResult, error) {
	if err := in.Validate(); err != nil {
		return SaveResult{}, err
	}

	uomID, err := s.store.UOMID(ctx, in.NetWeightUOM)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SaveResult{}, fmt.Errorf("%w: unknown unit of measure %q", ErrValidation, in.NetWeightUOM)
		}
		return SaveResult{}, err
	}

	var warnings []Warning

	// Changing a package weight silently re-values every balance recorded
	// against it, because weight and package count are stored together.
	if in.Version != nil {
		existing, err := s.packagingByCode(ctx, in.Code)
		if err == nil && existing.WeightInTon != in.WeightInTon() {
			warnings = append(warnings, Warning{
				Field: "netWeight",
				Message: fmt.Sprintf(
					"changing %s from %.6f t to %.6f t per package does not restate existing stock; "+
						"balances already posted keep their recorded weight",
					in.Code, existing.WeightInTon, in.WeightInTon()),
			})
		}
	}

	id, err := s.store.SavePackagingType(ctx, postgres.UpsertPackagingType{
		Code:           in.Code,
		Description:    in.Description,
		NetWeight:      in.NetWeight,
		NetWeightUOMID: uomID,
		WeightInTon:    in.WeightInTon(),
		IsBulk:         in.IsBulk,
		Status:         in.Status,
		Version:        in.Version,
		UpdatedBy:      auth.Actor(ctx, in.UpdatedBy),
	})
	if err != nil {
		return SaveResult{}, translateWriteError(err)
	}

	return SaveResult{ID: id, Created: in.Version == nil, Warnings: warnings}, nil
}

// ReplaceThresholdBands swaps a factory's alert bands for a new contiguous set.
func (s *Service) ReplaceThresholdBands(ctx context.Context, in ThresholdBandsInput) ([]capacity.ThresholdBand, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	rows := make([]postgres.ThresholdBandRow, 0, len(in.Bands))
	for _, b := range in.Bands {
		rows = append(rows, postgres.ThresholdBandRow{
			Code: b.Code, Name: b.Name, Severity: b.Severity,
			UIState: b.UIState, From: b.From, To: b.To,
		})
	}
	if err := s.store.ReplaceThresholdBands(ctx, in.FactoryID, rows, auth.Actor(ctx, in.UpdatedBy)); err != nil {
		return nil, err
	}
	return s.store.ListThresholdBands(ctx, in.FactoryID)
}

// --- helpers ---------------------------------------------------------------

func (s *Service) productByCode(ctx context.Context, code string) (domain.Product, error) {
	products, err := s.store.ListProducts(ctx)
	if err != nil {
		return domain.Product{}, err
	}
	for _, p := range products {
		if p.Code == code {
			return p, nil
		}
	}
	return domain.Product{}, fmt.Errorf("%w: unknown product %q", ErrValidation, code)
}

func (s *Service) packagingByCode(ctx context.Context, code string) (domain.PackagingType, error) {
	packagings, err := s.store.ListPackagingTypes(ctx)
	if err != nil {
		return domain.PackagingType{}, err
	}
	for _, p := range packagings {
		if p.Code == code {
			return p, nil
		}
	}
	return domain.PackagingType{}, fmt.Errorf("%w: unknown packaging type %q", ErrValidation, code)
}

// translateWriteError maps store-level failures onto the service's error
// vocabulary so handlers return the right status code.
func translateWriteError(err error) error {
	switch {
	case errors.Is(err, postgres.ErrVersionConflict):
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: no such record to update", ErrNotFound)
	default:
		return err
	}
}
