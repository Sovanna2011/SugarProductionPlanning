package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrVersionConflict means the caller's version no longer matches the stored
// row: somebody else changed it in between.
var ErrVersionConflict = errors.New("version conflict")

// ErrNoRows is returned when an update targets a row that does not exist.
var ErrNoRows = pgx.ErrNoRows

// UpsertStorageLocation input, resolved to ids by the service layer.
type UpsertStorageLocation struct {
	ID                     int64
	FactoryID              int64
	StorageCode            string
	StorageName            string
	StorageTypeID          int64
	PhysicalCapacity       float64
	CapacityUOMID          int64
	MinimumStockLevel      float64
	SafeCapacityPercentage float64
	AllowMixedProducts     bool
	AllowMixedBatches      bool
	Status                 string
	EffectiveFrom          *time.Time
	EffectiveTo            *time.Time
	Remark                 string
	Version                *int
	UpdatedBy              string
}

// SaveStorageLocation inserts a new location or updates an existing one.
//
// An update requires the caller's version to match, so two people editing the
// same warehouse cannot silently overwrite each other.
func (s *Store) SaveStorageLocation(ctx context.Context, in UpsertStorageLocation) (int64, error) {
	actor := in.UpdatedBy
	if actor == "" {
		actor = "SYSTEM"
	}
	from := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	if in.EffectiveFrom != nil {
		from = *in.EffectiveFrom
	}

	if in.Version == nil {
		var id int64
		err := s.pool.QueryRow(ctx, `
			INSERT INTO storage_locations (
				factory_id, storage_code, storage_name, storage_type_id,
				physical_capacity, capacity_uom_id, minimum_stock_level,
				safe_capacity_percentage, allow_mixed_products, allow_mixed_batches,
				status, effective_from, effective_to, remark, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15)
			RETURNING id`,
			in.FactoryID, in.StorageCode, in.StorageName, in.StorageTypeID,
			in.PhysicalCapacity, in.CapacityUOMID, in.MinimumStockLevel,
			in.SafeCapacityPercentage, in.AllowMixedProducts, in.AllowMixedBatches,
			in.Status, from, in.EffectiveTo, in.Remark, actor).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert storage location: %w", err)
		}
		return id, nil
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		UPDATE storage_locations SET
			storage_name = $3, storage_type_id = $4,
			physical_capacity = $5, capacity_uom_id = $6, minimum_stock_level = $7,
			safe_capacity_percentage = $8, allow_mixed_products = $9, allow_mixed_batches = $10,
			status = $11, effective_from = $12, effective_to = $13, remark = $14,
			updated_at = now(), updated_by = $15, version = version + 1
		WHERE factory_id = $1 AND storage_code = $2 AND version = $16
		RETURNING id`,
		in.FactoryID, in.StorageCode, in.StorageName, in.StorageTypeID,
		in.PhysicalCapacity, in.CapacityUOMID, in.MinimumStockLevel,
		in.SafeCapacityPercentage, in.AllowMixedProducts, in.AllowMixedBatches,
		in.Status, from, in.EffectiveTo, in.Remark, actor, *in.Version).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, s.explainUpdateMiss(ctx,
			`SELECT version FROM storage_locations WHERE factory_id = $1 AND storage_code = $2`,
			*in.Version, in.FactoryID, in.StorageCode)
	}
	if err != nil {
		return 0, fmt.Errorf("update storage location: %w", err)
	}
	return id, nil
}

// UpsertStorageProductCapacity input, resolved to ids by the service layer.
type UpsertStorageProductCapacity struct {
	StorageLocationID int64
	ProductID         int64
	PackagingTypeID   int64
	MaximumPackageQty *float64
	MaximumWeightQty  *float64
	MinimumStockQty   float64
	MaximumSafeQty    *float64
	WeightUOMID       int64
	EffectiveFrom     *time.Time
	EffectiveTo       *time.Time
	Status            string
	Remark            string
	Version           *int
	UpdatedBy         string
}

// SaveStorageProductCapacity inserts or updates one cell of the capacity
// matrix, keyed by location + product + packaging + effective_from.
func (s *Store) SaveStorageProductCapacity(ctx context.Context, in UpsertStorageProductCapacity) (int64, error) {
	actor := in.UpdatedBy
	if actor == "" {
		actor = "SYSTEM"
	}
	from := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	if in.EffectiveFrom != nil {
		from = *in.EffectiveFrom
	}

	if in.Version == nil {
		var id int64
		err := s.pool.QueryRow(ctx, `
			INSERT INTO storage_product_capacity (
				storage_location_id, product_id, packaging_type_id,
				maximum_package_quantity, maximum_weight_quantity, weight_uom_id,
				minimum_stock_quantity, maximum_safe_quantity,
				effective_from, effective_to, status, remark, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)
			RETURNING id`,
			in.StorageLocationID, in.ProductID, in.PackagingTypeID,
			in.MaximumPackageQty, in.MaximumWeightQty, in.WeightUOMID,
			in.MinimumStockQty, in.MaximumSafeQty,
			from, in.EffectiveTo, in.Status, in.Remark, actor).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert storage product capacity: %w", err)
		}
		return id, nil
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		UPDATE storage_product_capacity SET
			maximum_package_quantity = $4, maximum_weight_quantity = $5, weight_uom_id = $6,
			minimum_stock_quantity = $7, maximum_safe_quantity = $8,
			effective_to = $9, status = $10, remark = $11,
			updated_at = now(), updated_by = $12, version = version + 1
		WHERE storage_location_id = $1 AND product_id = $2 AND packaging_type_id = $3
		  AND effective_from = $13 AND version = $14
		RETURNING id`,
		in.StorageLocationID, in.ProductID, in.PackagingTypeID,
		in.MaximumPackageQty, in.MaximumWeightQty, in.WeightUOMID,
		in.MinimumStockQty, in.MaximumSafeQty,
		in.EffectiveTo, in.Status, in.Remark, actor, from, *in.Version).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, s.explainUpdateMiss(ctx, `
			SELECT version FROM storage_product_capacity
			WHERE storage_location_id = $1 AND product_id = $2 AND packaging_type_id = $3
			  AND effective_from = $4`,
			*in.Version, in.StorageLocationID, in.ProductID, in.PackagingTypeID, from)
	}
	if err != nil {
		return 0, fmt.Errorf("update storage product capacity: %w", err)
	}
	return id, nil
}

// UpsertPackagingType input.
type UpsertPackagingType struct {
	Code           string
	Description    string
	NetWeight      float64
	NetWeightUOMID int64
	WeightInTon    float64
	IsBulk         bool
	Status         string
	Version        *int
	UpdatedBy      string
}

// SavePackagingType inserts or updates a packaging master row.
func (s *Store) SavePackagingType(ctx context.Context, in UpsertPackagingType) (int64, error) {
	actor := in.UpdatedBy
	if actor == "" {
		actor = "SYSTEM"
	}

	if in.Version == nil {
		var id int64
		err := s.pool.QueryRow(ctx, `
			INSERT INTO packaging_types (
				code, description, net_weight, net_weight_uom_id, weight_in_ton,
				is_bulk, status, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
			RETURNING id`,
			in.Code, in.Description, in.NetWeight, in.NetWeightUOMID, in.WeightInTon,
			in.IsBulk, in.Status, actor).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert packaging type: %w", err)
		}
		return id, nil
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		UPDATE packaging_types SET
			description = $2, net_weight = $3, net_weight_uom_id = $4, weight_in_ton = $5,
			is_bulk = $6, status = $7, updated_at = now(), updated_by = $8,
			version = version + 1
		WHERE code = $1 AND version = $9
		RETURNING id`,
		in.Code, in.Description, in.NetWeight, in.NetWeightUOMID, in.WeightInTon,
		in.IsBulk, in.Status, actor, *in.Version).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, s.explainUpdateMiss(ctx,
			`SELECT version FROM packaging_types WHERE code = $1`, *in.Version, in.Code)
	}
	if err != nil {
		return 0, fmt.Errorf("update packaging type: %w", err)
	}
	return id, nil
}

// ThresholdBandRow is one band to store.
type ThresholdBandRow struct {
	Code      string
	Name      string
	Severity  string
	UIState   string
	From      float64
	To        *float64
	SortOrder int
}

// ReplaceThresholdBands swaps a factory's whole alert band set in one
// transaction. Bands only make sense as a contiguous cover, so they are
// replaced together rather than edited one at a time.
//
// factoryID of zero maintains the global default set.
func (s *Store) ReplaceThresholdBands(ctx context.Context, factoryID int64, bands []ThresholdBandRow, updatedBy string) error {
	actor := updatedBy
	if actor == "" {
		actor = "SYSTEM"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var factory *int64
	if factoryID > 0 {
		factory = &factoryID
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM capacity_threshold_levels
		WHERE factory_id IS NOT DISTINCT FROM $1`, factory); err != nil {
		return fmt.Errorf("clear threshold bands: %w", err)
	}

	for i, b := range bands {
		if _, err := tx.Exec(ctx, `
			INSERT INTO capacity_threshold_levels (
				factory_id, code, name, from_percentage, to_percentage,
				severity, ui_state, sort_order, created_by, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
			factory, b.Code, b.Name, b.From, b.To, b.Severity, b.UIState, (i+1)*10, actor); err != nil {
			return fmt.Errorf("insert threshold band %s: %w", b.Code, err)
		}
	}

	return tx.Commit(ctx)
}

// explainUpdateMiss turns an update that matched nothing into a useful error:
// either the row is gone, or somebody else has changed it.
func (s *Store) explainUpdateMiss(ctx context.Context, versionQuery string, wanted int, args ...any) error {
	var current int
	err := s.pool.QueryRow(ctx, versionQuery, args...).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: you supplied version %d but the record is at version %d",
		ErrVersionConflict, wanted, current)
}
