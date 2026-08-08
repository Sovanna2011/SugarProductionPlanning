package postgres

import (
	"context"
	"fmt"
	"time"
)

// UpsertDailyStoragePlan is one plan line to save.
type UpsertDailyStoragePlan struct {
	FactoryID            int64
	PlanDate             time.Time
	StorageLocationID    *int64
	StorageGroupID       *int64
	ProductID            *int64
	PackagingTypeID      *int64
	OpeningWeight        float64
	PlannedInWeight      float64
	PlannedOutWeight     float64
	PlannedClosingWeight float64
	OpeningPackages      float64
	PlannedInPackages    float64
	PlannedOutPackages   float64
	PlannedClosingPkgs   float64
	WeightUOMID          int64
	Status               string
	Remark               string
}

// SaveDailyStoragePlan inserts a plan line or replaces the existing one for
// the same day and scope.
//
// The conflict target matches the unique index on the table, which uses
// COALESCE so the optional product and packaging axes take part in the key.
func (s *Store) SaveDailyStoragePlan(ctx context.Context, in UpsertDailyStoragePlan) error {
	actor, err := s.ActorID(ctx)
	if err != nil {
		return err
	}

	// Attach the line to the season covering its date, when there is one.
	var seasonID *int64
	var id int64
	err = s.pool.QueryRow(ctx, `
		SELECT id FROM seasons
		WHERE factory_id = $1 AND $2 BETWEEN crushing_start AND season_end
		ORDER BY crushing_start DESC LIMIT 1`, in.FactoryID, in.PlanDate).Scan(&id)
	if err == nil {
		seasonID = &id
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO daily_storage_plans (
			season_id, factory_id, plan_date,
			storage_location_id, storage_group_id, product_id, packaging_type_id,
			opening_weight, planned_in_weight, planned_out_weight, planned_closing_weight,
			opening_packages, planned_in_packages, planned_out_packages, planned_closing_packages,
			weight_uom_id, status, remark, created_by, changed_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$19)
		ON CONFLICT (plan_date,
		             COALESCE(storage_location_id, 0),
		             COALESCE(storage_group_id, 0),
		             COALESCE(product_id, 0),
		             COALESCE(packaging_type_id, 0))
		DO UPDATE SET
			opening_weight           = EXCLUDED.opening_weight,
			planned_in_weight        = EXCLUDED.planned_in_weight,
			planned_out_weight       = EXCLUDED.planned_out_weight,
			planned_closing_weight   = EXCLUDED.planned_closing_weight,
			opening_packages         = EXCLUDED.opening_packages,
			planned_in_packages      = EXCLUDED.planned_in_packages,
			planned_out_packages     = EXCLUDED.planned_out_packages,
			planned_closing_packages = EXCLUDED.planned_closing_packages,
			status     = EXCLUDED.status,
			remark     = EXCLUDED.remark,
			changed_by = EXCLUDED.changed_by,
			version    = daily_storage_plans.version + 1`,
		seasonID, in.FactoryID, in.PlanDate,
		in.StorageLocationID, in.StorageGroupID, in.ProductID, in.PackagingTypeID,
		in.OpeningWeight, in.PlannedInWeight, in.PlannedOutWeight, in.PlannedClosingWeight,
		in.OpeningPackages, in.PlannedInPackages, in.PlannedOutPackages, in.PlannedClosingPkgs,
		in.WeightUOMID, in.Status, in.Remark, actor)
	if err != nil {
		return fmt.Errorf("save daily storage plan: %w", err)
	}
	return nil
}
