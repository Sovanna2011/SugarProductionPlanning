package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// ListSeasons returns the crushing seasons for a factory.
func (s *Store) ListSeasons(ctx context.Context, factoryID int64) ([]domain.Season, error) {
	var w filter
	if factoryID > 0 {
		w.add("factory_id = ?", factoryID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, factory_id, code, name, crushing_start, crushing_end, season_end,
		       cane_target, recovery_rate, status
		FROM seasons WHERE TRUE`+w.where()+`
		ORDER BY crushing_start DESC`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.Season, error) {
		var v domain.Season
		err := r.Scan(&v.ID, &v.FactoryID, &v.Code, &v.Name, &v.CrushingStart, &v.CrushingEnd,
			&v.SeasonEnd, &v.CaneTarget, &v.RecoveryRate, &v.Status)
		return v, err
	})
}

// ProductionPlanFilter narrows a daily production plan query.
type ProductionPlanFilter struct {
	FactoryID int64
	SeasonID  int64
	From, To  time.Time
}

// ListDailyProductionPlans returns the daily production plan.
func (s *Store) ListDailyProductionPlans(ctx context.Context, f ProductionPlanFilter) ([]domain.DailyProductionPlan, error) {
	var w filter
	if f.FactoryID > 0 {
		w.add("factory_id = ?", f.FactoryID)
	}
	if f.SeasonID > 0 {
		w.add("season_id = ?", f.SeasonID)
	}
	if !f.From.IsZero() {
		w.add("plan_date >= ?", f.From)
	}
	if !f.To.IsZero() {
		w.add("plan_date <= ?", f.To)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, season_id, factory_id, plan_date, day_no,
		       cane_target, cane_actual, raw_sugar_target, raw_sugar_actual,
		       raw_to_remelt_target, raw_silo_to_remelt_target, raw_packing_target,
		       refined_sugar_target, white_sugar_target, super_refined_target,
		       quota_sales_target, is_cleaning_day, remark
		FROM daily_production_plans WHERE TRUE`+w.where()+`
		ORDER BY plan_date`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.DailyProductionPlan, error) {
		var v domain.DailyProductionPlan
		err := r.Scan(&v.ID, &v.SeasonID, &v.FactoryID, &v.PlanDate, &v.DayNo,
			&v.CaneTarget, &v.CaneActual, &v.RawSugarTarget, &v.RawSugarActual,
			&v.RawToRemeltTarget, &v.RawSiloToRemeltTarget, &v.RawPackingTarget,
			&v.RefinedSugarTarget, &v.WhiteSugarTarget, &v.SuperRefinedTarget,
			&v.QuotaSalesTarget, &v.IsCleaningDay, &v.Remark)
		return v, err
	})
}

// StoragePlanFilter narrows a daily storage plan query.
type StoragePlanFilter struct {
	FactoryID         int64
	StorageLocationID int64
	StorageGroupID    int64
	ScopeCode         string
	From, To          time.Time
}

// ListDailyStoragePlans returns the daily storage plan lines, resolving each
// line's scope to a code and name whether it targets a location or a pool.
func (s *Store) ListDailyStoragePlans(ctx context.Context, f StoragePlanFilter) ([]domain.DailyStoragePlan, error) {
	var w filter
	if f.FactoryID > 0 {
		w.add("d.factory_id = ?", f.FactoryID)
	}
	if f.StorageLocationID > 0 {
		w.add("d.storage_location_id = ?", f.StorageLocationID)
	}
	if f.StorageGroupID > 0 {
		w.add("d.storage_group_id = ?", f.StorageGroupID)
	}
	if f.ScopeCode != "" {
		w.add("COALESCE(l.storage_code, g.group_code) = ?", f.ScopeCode)
	}
	if !f.From.IsZero() {
		w.add("d.plan_date >= ?", f.From)
	}
	if !f.To.IsZero() {
		w.add("d.plan_date <= ?", f.To)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.factory_id, d.plan_date,
		       d.storage_location_id, d.storage_group_id,
		       COALESCE(l.storage_code, g.group_code),
		       COALESCE(l.storage_name, g.group_name),
		       d.product_id, COALESCE(p.code, ''),
		       d.packaging_type_id, COALESCE(k.code, ''),
		       d.opening_weight, d.planned_in_weight, d.planned_out_weight, d.planned_closing_weight,
		       d.opening_packages, d.planned_in_packages, d.planned_out_packages, d.planned_closing_packages,
		       u.code, d.status, d.remark
		FROM daily_storage_plans d
		LEFT JOIN storage_locations l ON l.id = d.storage_location_id
		LEFT JOIN storage_groups g    ON g.id = d.storage_group_id
		LEFT JOIN products p          ON p.id = d.product_id
		LEFT JOIN packaging_types k   ON k.id = d.packaging_type_id
		JOIN uoms u ON u.id = d.weight_uom_id
		WHERE TRUE`+w.where()+`
		ORDER BY d.plan_date, COALESCE(l.storage_code, g.group_code)`, w.args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (domain.DailyStoragePlan, error) {
		var v domain.DailyStoragePlan
		err := r.Scan(&v.ID, &v.FactoryID, &v.PlanDate,
			&v.StorageLocationID, &v.StorageGroupID, &v.ScopeCode, &v.ScopeName,
			&v.ProductID, &v.ProductCode, &v.PackagingTypeID, &v.PackagingCode,
			&v.OpeningWeight, &v.PlannedInWeight, &v.PlannedOutWeight, &v.PlannedClosingWeight,
			&v.OpeningPackages, &v.PlannedInPackages, &v.PlannedOutPackages, &v.PlannedClosingPkgs,
			&v.WeightUOM, &v.Status, &v.Remark)
		return v, err
	})
}
