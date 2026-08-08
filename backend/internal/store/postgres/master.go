package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// ListFactories returns every active factory with its company name.
func (s *Store) ListFactories(ctx context.Context) ([]domain.Factory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id, f.company_id, c.name, f.code, f.name
		FROM factories f
		JOIN companies c ON c.id = f.company_id
		WHERE f.status = 'ACTIVE'
		ORDER BY f.code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.Factory, error) {
		var v domain.Factory
		err := r.Scan(&v.ID, &v.CompanyID, &v.CompanyName, &v.Code, &v.Name)
		return v, err
	})
}

// ListStorageTypes returns the storage type master.
func (s *Store) ListStorageTypes(ctx context.Context) ([]domain.StorageType, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, tracks_packages, description
		FROM storage_types
		WHERE status = 'ACTIVE'
		ORDER BY code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.StorageType, error) {
		var v domain.StorageType
		err := r.Scan(&v.ID, &v.Code, &v.Name, &v.TracksPackages, &v.Description)
		return v, err
	})
}

// StorageLocationFilter narrows a storage location query.
type StorageLocationFilter struct {
	FactoryID       int64
	StorageTypeCode string
	StorageCode     string
	IncludeInactive bool
}

const storageLocationColumns = `
	l.id, l.factory_id, l.storage_code, l.storage_name,
	l.storage_type_id, t.code, t.name, t.tracks_packages,
	l.physical_capacity, u.code,
	l.minimum_stock_level, l.safe_capacity_percentage,
	l.allow_mixed_products, l.allow_mixed_batches,
	l.status, l.effective_from, l.effective_to, l.remark, l.version`

func scanStorageLocation(r pgx.Rows) (domain.StorageLocation, error) {
	var v domain.StorageLocation
	err := r.Scan(&v.ID, &v.FactoryID, &v.StorageCode, &v.StorageName,
		&v.StorageTypeID, &v.StorageTypeCode, &v.StorageTypeName, &v.TracksPackages,
		&v.PhysicalCapacity, &v.CapacityUOM,
		&v.MinimumStockLevel, &v.SafeCapacityPercentage,
		&v.AllowMixedProducts, &v.AllowMixedBatches,
		&v.Status, &v.EffectiveFrom, &v.EffectiveTo, &v.Remark, &v.Version)
	return v, err
}

// ListStorageLocations returns storage locations matching the filter.
func (s *Store) ListStorageLocations(ctx context.Context, f StorageLocationFilter) ([]domain.StorageLocation, error) {
	var w filter
	if f.FactoryID > 0 {
		w.add("l.factory_id = ?", f.FactoryID)
	}
	if f.StorageTypeCode != "" {
		w.add("t.code = ?", f.StorageTypeCode)
	}
	if f.StorageCode != "" {
		w.add("l.storage_code = ?", f.StorageCode)
	}
	if !f.IncludeInactive {
		w.add("l.status = 'ACTIVE'")
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+storageLocationColumns+`
		FROM storage_locations l
		JOIN storage_types t ON t.id = l.storage_type_id
		JOIN uoms u ON u.id = l.capacity_uom_id
		WHERE TRUE`+w.where()+`
		ORDER BY t.code, l.storage_code`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanStorageLocation)
}

// GetStorageLocation looks a location up by its code within a factory.
func (s *Store) GetStorageLocation(ctx context.Context, factoryID int64, code string) (domain.StorageLocation, error) {
	locs, err := s.ListStorageLocations(ctx, StorageLocationFilter{
		FactoryID: factoryID, StorageCode: code, IncludeInactive: true,
	})
	if err != nil {
		return domain.StorageLocation{}, err
	}
	if len(locs) == 0 {
		return domain.StorageLocation{}, pgx.ErrNoRows
	}
	return locs[0], nil
}

// ListStorageGroups returns pooled storage groups with their members.
func (s *Store) ListStorageGroups(ctx context.Context, factoryID int64) ([]domain.StorageGroup, error) {
	var w filter
	if factoryID > 0 {
		w.add("g.factory_id = ?", factoryID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.factory_id, g.group_code, g.group_name, g.remark
		FROM storage_groups g
		WHERE g.status = 'ACTIVE'`+w.where()+`
		ORDER BY g.group_code`, w.args...)
	if err != nil {
		return nil, err
	}
	groups, err := collect(rows, func(r pgx.Rows) (domain.StorageGroup, error) {
		var v domain.StorageGroup
		err := r.Scan(&v.ID, &v.FactoryID, &v.GroupCode, &v.GroupName, &v.Remark)
		return v, err
	})
	if err != nil {
		return nil, err
	}

	for i := range groups {
		memberRows, err := s.pool.Query(ctx, `
			SELECT `+storageLocationColumns+`
			FROM storage_group_members m
			JOIN storage_locations l ON l.id = m.storage_location_id
			JOIN storage_types t ON t.id = l.storage_type_id
			JOIN uoms u ON u.id = l.capacity_uom_id
			WHERE m.storage_group_id = $1
			ORDER BY l.storage_code`, groups[i].ID)
		if err != nil {
			return nil, err
		}
		members, err := collect(memberRows, scanStorageLocation)
		if err != nil {
			return nil, err
		}
		groups[i].Members = members
	}
	return groups, nil
}

// GetStorageGroup looks a pooled group up by code.
func (s *Store) GetStorageGroup(ctx context.Context, factoryID int64, code string) (domain.StorageGroup, error) {
	groups, err := s.ListStorageGroups(ctx, factoryID)
	if err != nil {
		return domain.StorageGroup{}, err
	}
	for _, g := range groups {
		if g.GroupCode == code {
			return g, nil
		}
	}
	return domain.StorageGroup{}, pgx.ErrNoRows
}

// ListProducts returns the product master.
func (s *Store) ListProducts(ctx context.Context) ([]domain.Product, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, code, name, product_group, is_bulk_liquid, status
		FROM products WHERE status = 'ACTIVE' ORDER BY product_group, code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.Product, error) {
		var v domain.Product
		err := r.Scan(&v.ID, &v.Code, &v.Name, &v.ProductGroup, &v.IsBulkLiquid, &v.Status)
		return v, err
	})
}

// ListPackagingTypes returns the packaging master.
func (s *Store) ListPackagingTypes(ctx context.Context) ([]domain.PackagingType, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT k.id, k.code, k.description, k.net_weight, u.code, k.weight_in_ton, k.is_bulk, k.status, k.version
		FROM packaging_types k
		JOIN uoms u ON u.id = k.net_weight_uom_id
		WHERE k.status = 'ACTIVE'
		ORDER BY k.weight_in_ton, k.code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.PackagingType, error) {
		var v domain.PackagingType
		err := r.Scan(&v.ID, &v.Code, &v.Description, &v.NetWeight, &v.NetWeightUOM,
			&v.WeightInTon, &v.IsBulk, &v.Status, &v.Version)
		return v, err
	})
}

// ListProductPackaging returns the product + packaging combinations.
func (s *Store) ListProductPackaging(ctx context.Context) ([]domain.ProductPackaging, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pp.id, p.id, p.code, p.name, k.id, k.code,
		       pp.sku_code, pp.sku_name, pp.is_default, pp.status
		FROM product_packaging pp
		JOIN products p ON p.id = pp.product_id
		JOIN packaging_types k ON k.id = pp.packaging_type_id
		WHERE pp.status = 'ACTIVE'
		ORDER BY p.code, k.weight_in_ton`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.ProductPackaging, error) {
		var v domain.ProductPackaging
		err := r.Scan(&v.ID, &v.ProductID, &v.ProductCode, &v.ProductName,
			&v.PackagingID, &v.PackagingCode, &v.SKUCode, &v.SKUName, &v.IsDefault, &v.Status)
		return v, err
	})
}

// CapacityFilter narrows a storage product capacity query.
type CapacityFilter struct {
	FactoryID         int64
	StorageLocationID int64
	StorageCode       string
	ProductID         int64
	PackagingTypeID   int64
	// OnDate selects the row in force on that date. Zero means today.
	OnDate time.Time
}

// ListStorageProductCapacity returns the warehouse + product + packaging
// capacity matrix in force on the requested date.
func (s *Store) ListStorageProductCapacity(ctx context.Context, f CapacityFilter) ([]domain.StorageProductCapacity, error) {
	onDate := f.OnDate
	if onDate.IsZero() {
		onDate = time.Now()
	}

	var w filter
	w.add("c.effective_from <= ?", onDate)
	w.add("(c.effective_to IS NULL OR c.effective_to >= ?)", onDate)
	if f.FactoryID > 0 {
		w.add("l.factory_id = ?", f.FactoryID)
	}
	if f.StorageLocationID > 0 {
		w.add("c.storage_location_id = ?", f.StorageLocationID)
	}
	if f.StorageCode != "" {
		w.add("l.storage_code = ?", f.StorageCode)
	}
	if f.ProductID > 0 {
		w.add("c.product_id = ?", f.ProductID)
	}
	if f.PackagingTypeID > 0 {
		w.add("c.packaging_type_id = ?", f.PackagingTypeID)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.storage_location_id, l.storage_code, l.storage_name,
		       c.product_id, p.code, p.name,
		       c.packaging_type_id, k.code, k.description, k.weight_in_ton,
		       c.maximum_package_quantity, c.maximum_weight_quantity,
		       c.minimum_stock_quantity, c.maximum_safe_quantity, u.code,
		       c.effective_from, c.effective_to, c.status, c.remark, c.version
		FROM storage_product_capacity c
		JOIN storage_locations l ON l.id = c.storage_location_id
		JOIN products p ON p.id = c.product_id
		JOIN packaging_types k ON k.id = c.packaging_type_id
		JOIN uoms u ON u.id = c.weight_uom_id
		WHERE c.status = 'ACTIVE'`+w.where()+`
		ORDER BY l.storage_code, p.code, k.weight_in_ton`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.StorageProductCapacity, error) {
		var v domain.StorageProductCapacity
		err := r.Scan(&v.ID, &v.StorageLocationID, &v.StorageCode, &v.StorageName,
			&v.ProductID, &v.ProductCode, &v.ProductName,
			&v.PackagingTypeID, &v.PackagingCode, &v.PackagingDescription, &v.WeightPerPackage,
			&v.MaximumPackageQty, &v.MaximumWeightQty,
			&v.MinimumStockQty, &v.MaximumSafeQty, &v.WeightUOM,
			&v.EffectiveFrom, &v.EffectiveTo, &v.Status, &v.Remark, &v.Version)
		return v, err
	})
}

// ListThresholdBands returns the alert bands for a factory, falling back to the
// global set when the factory has none of its own.
func (s *Store) ListThresholdBands(ctx context.Context, factoryID int64) ([]capacity.ThresholdBand, error) {
	query := `
		SELECT code, name, severity, ui_state, from_percentage, to_percentage
		FROM capacity_threshold_levels
		WHERE status = 'ACTIVE' AND factory_id = $1
		ORDER BY sort_order, from_percentage`

	rows, err := s.pool.Query(ctx, query, factoryID)
	if err != nil {
		return nil, err
	}
	bands, err := collect(rows, scanBand)
	if err != nil {
		return nil, err
	}
	if len(bands) > 0 {
		return bands, nil
	}

	rows, err = s.pool.Query(ctx, `
		SELECT code, name, severity, ui_state, from_percentage, to_percentage
		FROM capacity_threshold_levels
		WHERE status = 'ACTIVE' AND factory_id IS NULL
		ORDER BY sort_order, from_percentage`)
	if err != nil {
		return nil, err
	}
	bands, err = collect(rows, scanBand)
	if err != nil {
		return nil, err
	}
	if len(bands) == 0 {
		// Never let a missing configuration silently report everything normal.
		return capacity.DefaultBands, nil
	}
	return bands, nil
}

func scanBand(r pgx.Rows) (capacity.ThresholdBand, error) {
	var v capacity.ThresholdBand
	err := r.Scan(&v.Code, &v.Name, &v.Severity, &v.UIState, &v.From, &v.To)
	return v, err
}
