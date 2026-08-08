package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// BalanceFilter narrows an inventory balance query.
type BalanceFilter struct {
	FactoryID         int64
	StorageLocationID int64
	StorageCode       string
	StorageTypeCode   string
	ProductID         int64
	PackagingTypeID   int64
	BatchNo           string
	// NonZeroOnly drops rows that hold nothing.
	NonZeroOnly bool
}

// ListBalances returns inventory balances keyed by factory + storage +
// product + packaging + batch (requirement section 23).
func (s *Store) ListBalances(ctx context.Context, f BalanceFilter) ([]domain.InventoryBalance, error) {
	var w filter
	if f.FactoryID > 0 {
		w.add("b.factory_id = ?", f.FactoryID)
	}
	if f.StorageLocationID > 0 {
		w.add("b.storage_location_id = ?", f.StorageLocationID)
	}
	if f.StorageCode != "" {
		w.add("l.storage_code = ?", f.StorageCode)
	}
	if f.StorageTypeCode != "" {
		w.add("t.code = ?", f.StorageTypeCode)
	}
	if f.ProductID > 0 {
		w.add("b.product_id = ?", f.ProductID)
	}
	if f.PackagingTypeID > 0 {
		w.add("b.packaging_type_id = ?", f.PackagingTypeID)
	}
	if f.BatchNo != "" {
		w.add("b.batch_no = ?", f.BatchNo)
	}
	if f.NonZeroOnly {
		w.add("(b.package_quantity <> 0 OR b.weight_quantity <> 0)")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.factory_id, b.storage_location_id, l.storage_code, l.storage_name,
		       b.product_id, p.code, p.name,
		       b.packaging_type_id, k.code, b.batch_no,
		       b.package_quantity, b.weight_quantity,
		       b.reserved_package_quantity, b.reserved_weight_quantity,
		       u.code, b.last_movement_date
		FROM inventory_balances b
		JOIN storage_locations l ON l.id = b.storage_location_id
		JOIN storage_types t ON t.id = l.storage_type_id
		JOIN products p ON p.id = b.product_id
		JOIN packaging_types k ON k.id = b.packaging_type_id
		JOIN uoms u ON u.id = b.weight_uom_id
		WHERE TRUE`+w.where()+`
		ORDER BY l.storage_code, p.code, k.weight_in_ton, b.batch_no`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.InventoryBalance, error) {
		var v domain.InventoryBalance
		err := r.Scan(&v.ID, &v.FactoryID, &v.StorageLocationID, &v.StorageCode, &v.StorageName,
			&v.ProductID, &v.ProductCode, &v.ProductName,
			&v.PackagingTypeID, &v.PackagingCode, &v.BatchNo,
			&v.PackageQty, &v.WeightQty, &v.ReservedPackage, &v.ReservedWeight,
			&v.WeightUOM, &v.LastMovementDate)
		return v, err
	})
}

// LocationTotals is the aggregate position of one storage location, used by
// the physical capacity check (requirement section 24.1).
type LocationTotals struct {
	TotalWeight    float64
	TotalPackages  float64
	ReservedWeight float64
	ProductIDs     []int64
}

// LocationTotal sums everything held in a location, across all products.
func (s *Store) LocationTotal(ctx context.Context, locationID int64) (LocationTotals, error) {
	var t LocationTotals
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(weight_quantity), 0),
		       COALESCE(SUM(package_quantity), 0),
		       COALESCE(ARRAY_AGG(DISTINCT product_id) FILTER (WHERE weight_quantity <> 0 OR package_quantity <> 0), '{}')
		FROM inventory_balances
		WHERE storage_location_id = $1`, locationID).
		Scan(&t.TotalWeight, &t.TotalPackages, &t.ProductIDs)
	return t, err
}

// LocationTotalsByID returns the aggregate for every location in one query, so
// the dashboard does not issue one round trip per warehouse.
func (s *Store) LocationTotalsByID(ctx context.Context, factoryID int64) (map[int64]LocationTotals, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT storage_location_id,
		       COALESCE(SUM(weight_quantity), 0),
		       COALESCE(SUM(package_quantity), 0),
		       COALESCE(SUM(reserved_weight_quantity), 0)
		FROM inventory_balances
		WHERE ($1 = 0 OR factory_id = $1)
		GROUP BY storage_location_id`, factoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]LocationTotals{}
	for rows.Next() {
		var id int64
		var t LocationTotals
		if err := rows.Scan(&id, &t.TotalWeight, &t.TotalPackages, &t.ReservedWeight); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// ProductStock is the stock on hand for one product + packaging in one
// location, summed across batches.
func (s *Store) ProductStock(ctx context.Context, locationID, productID, packagingID int64) (packages, weight float64, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(package_quantity), 0), COALESCE(SUM(weight_quantity), 0)
		FROM inventory_balances
		WHERE storage_location_id = $1 AND product_id = $2 AND packaging_type_id = $3`,
		locationID, productID, packagingID).Scan(&packages, &weight)
	return
}

// ListMovementTypes returns the movement type master.
func (s *Store) ListMovementTypes(ctx context.Context) ([]domain.MovementType, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, direction, requires_capacity_check
		FROM movement_types WHERE status = 'ACTIVE' ORDER BY direction, code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.MovementType, error) {
		var v domain.MovementType
		err := r.Scan(&v.ID, &v.Code, &v.Name, &v.Direction, &v.RequiresCapacityCheck)
		return v, err
	})
}

// GetMovementType looks a movement type up by code.
func (s *Store) GetMovementType(ctx context.Context, code string) (domain.MovementType, error) {
	var v domain.MovementType
	err := s.pool.QueryRow(ctx, `
		SELECT id, code, name, direction, requires_capacity_check
		FROM movement_types WHERE code = $1 AND status = 'ACTIVE'`, code).
		Scan(&v.ID, &v.Code, &v.Name, &v.Direction, &v.RequiresCapacityCheck)
	return v, err
}

// NewMovement is the input to PostMovement.
type NewMovement struct {
	FactoryID              int64
	MovementDate           time.Time
	MovementTypeID         int64
	StorageLocationID      int64
	ProductID              int64
	PackagingTypeID        int64
	BatchNo                string
	PackageQty             float64
	WeightQty              float64
	WeightUOMID            int64
	TransferRef            string
	ReferenceDoc           string
	Remark                 string
	CapacityOverride       bool
	CapacityOverrideReason string
	CreatedBy              string
}

// PostMovement writes the ledger line and applies it to the balance in one
// transaction, so a movement can never be recorded without moving stock.
//
// The balance is upserted on its natural key; actual closing stock is therefore
// always the sum of posted movements and is never keyed in
// (requirement section 16).
func (s *Store) PostMovement(ctx context.Context, m NewMovement) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	actor := m.CreatedBy
	if actor == "" {
		actor = "SYSTEM"
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_movements (
			factory_id, movement_date, movement_type_id, storage_location_id,
			product_id, packaging_type_id, batch_no,
			package_quantity, weight_quantity, weight_uom_id,
			transfer_ref, reference_doc, remark,
			posted, posted_at, posted_by,
			capacity_override, capacity_override_reason,
			created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,TRUE,now(),$14,$15,$16,$14,$14)
		RETURNING id`,
		m.FactoryID, m.MovementDate, m.MovementTypeID, m.StorageLocationID,
		m.ProductID, m.PackagingTypeID, m.BatchNo,
		m.PackageQty, m.WeightQty, m.WeightUOMID,
		m.TransferRef, m.ReferenceDoc, m.Remark,
		actor, m.CapacityOverride, m.CapacityOverrideReason).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert movement: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO inventory_balances (
			factory_id, storage_location_id, product_id, packaging_type_id, batch_no,
			package_quantity, weight_quantity, weight_uom_id, last_movement_date,
			created_by, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		ON CONFLICT (storage_location_id, product_id, packaging_type_id, batch_no)
		DO UPDATE SET
			package_quantity   = inventory_balances.package_quantity + EXCLUDED.package_quantity,
			weight_quantity    = inventory_balances.weight_quantity  + EXCLUDED.weight_quantity,
			last_movement_date = GREATEST(
				COALESCE(inventory_balances.last_movement_date, EXCLUDED.last_movement_date),
				EXCLUDED.last_movement_date),
			updated_at = now(),
			updated_by = EXCLUDED.updated_by,
			version    = inventory_balances.version + 1`,
		m.FactoryID, m.StorageLocationID, m.ProductID, m.PackagingTypeID, m.BatchNo,
		m.PackageQty, m.WeightQty, m.WeightUOMID, m.MovementDate, actor)
	if err != nil {
		return 0, fmt.Errorf("apply balance: %w", err)
	}

	return id, tx.Commit(ctx)
}

// MovementFilter narrows a movement query.
type MovementFilter struct {
	FactoryID         int64
	StorageLocationID int64
	From, To          time.Time
	Limit             int
}

// ListMovements returns the ledger, most recent first.
func (s *Store) ListMovements(ctx context.Context, f MovementFilter) ([]domain.InventoryMovement, error) {
	var w filter
	if f.FactoryID > 0 {
		w.add("m.factory_id = ?", f.FactoryID)
	}
	if f.StorageLocationID > 0 {
		w.add("m.storage_location_id = ?", f.StorageLocationID)
	}
	if !f.From.IsZero() {
		w.add("m.movement_date >= ?", f.From)
	}
	if !f.To.IsZero() {
		w.add("m.movement_date <= ?", f.To)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	// Bound and passed as a parameter rather than concatenated, so this query
	// needs no reasoning about the value's provenance.
	w.args = append(w.args, limit)
	limitPlaceholder := fmt.Sprintf("$%d", len(w.args))

	rows, err := s.pool.Query(ctx, `
		SELECT m.id, m.factory_id, m.movement_date, m.movement_type_id, mt.code, mt.direction,
		       m.storage_location_id, l.storage_code,
		       m.product_id, p.code, m.packaging_type_id, k.code, m.batch_no,
		       m.package_quantity, m.weight_quantity, u.code,
		       m.transfer_ref, m.reference_doc, m.remark,
		       m.posted, m.posted_at, COALESCE(m.posted_by, ''),
		       m.capacity_override, m.capacity_override_reason
		FROM inventory_movements m
		JOIN movement_types mt ON mt.id = m.movement_type_id
		JOIN storage_locations l ON l.id = m.storage_location_id
		JOIN products p ON p.id = m.product_id
		JOIN packaging_types k ON k.id = m.packaging_type_id
		JOIN uoms u ON u.id = m.weight_uom_id
		WHERE TRUE`+w.where()+`
		ORDER BY m.movement_date DESC, m.id DESC
		LIMIT `+limitPlaceholder, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.InventoryMovement, error) {
		var v domain.InventoryMovement
		err := r.Scan(&v.ID, &v.FactoryID, &v.MovementDate, &v.MovementTypeID, &v.MovementTypeCode, &v.Direction,
			&v.StorageLocationID, &v.StorageCode,
			&v.ProductID, &v.ProductCode, &v.PackagingTypeID, &v.PackagingCode, &v.BatchNo,
			&v.PackageQty, &v.WeightQty, &v.WeightUOM,
			&v.TransferRef, &v.ReferenceDoc, &v.Remark,
			&v.Posted, &v.PostedAt, &v.PostedBy,
			&v.CapacityOverride, &v.CapacityOverrideReason)
		return v, err
	})
}

// DayActuals is the posted in/out for one scope on one day, used by the plan
// versus actual comparison (requirement section 17).
type DayActuals struct {
	InWeight    float64
	OutWeight   float64
	InPackages  float64
	OutPackages float64
}

// ActualsForLocations returns posted movement totals per location for a date.
func (s *Store) ActualsForLocations(ctx context.Context, factoryID int64, date time.Time) (map[int64]DayActuals, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT storage_location_id,
		       COALESCE(SUM(weight_quantity)  FILTER (WHERE weight_quantity  > 0), 0),
		       COALESCE(SUM(-weight_quantity) FILTER (WHERE weight_quantity  < 0), 0),
		       COALESCE(SUM(package_quantity) FILTER (WHERE package_quantity > 0), 0),
		       COALESCE(SUM(-package_quantity) FILTER (WHERE package_quantity < 0), 0)
		FROM inventory_movements
		WHERE posted AND movement_date = $1 AND ($2 = 0 OR factory_id = $2)
		GROUP BY storage_location_id`, date, factoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]DayActuals{}
	for rows.Next() {
		var id int64
		var a DayActuals
		if err := rows.Scan(&id, &a.InWeight, &a.OutWeight, &a.InPackages, &a.OutPackages); err != nil {
			return nil, err
		}
		out[id] = a
	}
	return out, rows.Err()
}

// BalanceAsOf returns each location's closing weight as at the end of a date,
// derived from posted movements alone (requirement section 16).
func (s *Store) BalanceAsOf(ctx context.Context, factoryID int64, date time.Time) (map[int64]float64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT storage_location_id, COALESCE(SUM(weight_quantity), 0)
		FROM inventory_movements
		WHERE posted AND movement_date <= $1 AND ($2 = 0 OR factory_id = $2)
		GROUP BY storage_location_id`, date, factoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]float64{}
	for rows.Next() {
		var id int64
		var w float64
		if err := rows.Scan(&id, &w); err != nil {
			return nil, err
		}
		out[id] = w
	}
	return out, rows.Err()
}

// UOMID resolves a unit of measure code to its id.
func (s *Store) UOMID(ctx context.Context, code string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM uoms WHERE code = $1`, code).Scan(&id)
	return id, err
}
