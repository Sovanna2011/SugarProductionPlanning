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

// StoragePlanLineInput is one planned day for one storage scope
// (requirement section 15).
//
// Closing stock is never supplied: it is always
//
//	Planned Closing = Planned Opening + Planned In - Planned Out
//
// which is the same reason actual closing stock is never keyed in either.
type StoragePlanLineInput struct {
	FactoryID int64     `json:"factoryId"`
	PlanDate  time.Time `json:"planDate"`
	// ScopeCode is a storage location code or a storage group code.
	ScopeCode     string `json:"scopeCode"`
	ProductCode   string `json:"productCode,omitempty"`
	PackagingCode string `json:"packagingCode,omitempty"`

	// OpeningWeight may be omitted, in which case the previous day's planned
	// closing for the same scope is carried forward.
	OpeningWeight    *float64 `json:"openingWeight,omitempty"`
	PlannedInWeight  float64  `json:"plannedInWeight"`
	PlannedOutWeight float64  `json:"plannedOutWeight"`

	OpeningPackages    *float64 `json:"openingPackages,omitempty"`
	PlannedInPackages  float64  `json:"plannedInPackages"`
	PlannedOutPackages float64  `json:"plannedOutPackages"`

	Status    string `json:"status"`
	Remark    string `json:"remark"`
	UpdatedBy string `json:"updatedBy"`
}

// Validate applies the rules that do not need the database.
func (in *StoragePlanLineInput) Validate() error {
	if in.FactoryID <= 0 {
		return fmt.Errorf("%w: factoryId is required", ErrValidation)
	}
	if in.ScopeCode == "" {
		return fmt.Errorf("%w: scopeCode is required", ErrValidation)
	}
	if in.PlanDate.IsZero() {
		return fmt.Errorf("%w: planDate is required", ErrValidation)
	}
	if in.PlannedInWeight < 0 || in.PlannedOutWeight < 0 {
		return fmt.Errorf("%w: planned in and out must be positive magnitudes", ErrValidation)
	}
	if in.PlannedInPackages < 0 || in.PlannedOutPackages < 0 {
		return fmt.Errorf("%w: planned in and out must be positive magnitudes", ErrValidation)
	}
	if in.PackagingCode != "" && in.ProductCode == "" {
		return fmt.Errorf("%w: packagingCode needs a productCode alongside it", ErrValidation)
	}

	switch in.Status {
	case "":
		in.Status = "APPROVED"
	case "DRAFT", "APPROVED", "CLOSED":
	default:
		return fmt.Errorf("%w: status must be DRAFT, APPROVED or CLOSED, got %q", ErrValidation, in.Status)
	}
	return nil
}

// StoragePlanResult reports a saved plan line and how it sits against capacity.
type StoragePlanResult struct {
	PlanDate       string    `json:"planDate"`
	ScopeCode      string    `json:"scopeCode"`
	OpeningWeight  float64   `json:"openingWeight"`
	PlannedIn      float64   `json:"plannedInWeight"`
	PlannedOut     float64   `json:"plannedOutWeight"`
	PlannedClosing float64   `json:"plannedClosingWeight"`
	Capacity       float64   `json:"capacity"`
	UtilizationPct float64   `json:"utilizationPercentage"`
	Warnings       []Warning `json:"warnings,omitempty"`
}

// SaveStoragePlanLine creates or replaces one line of the daily storage plan.
func (s *Service) SaveStoragePlanLine(ctx context.Context, in StoragePlanLineInput) (StoragePlanResult, error) {
	if err := in.Validate(); err != nil {
		return StoragePlanResult{}, err
	}

	// Resolve the scope: a location, or a pooled group.
	var (
		locationID, groupID *int64
		scopeCapacity       float64
		safePct             = 100.0
	)
	if loc, err := s.store.GetStorageLocation(ctx, in.FactoryID, in.ScopeCode); err == nil {
		id := loc.ID
		locationID = &id
		scopeCapacity = loc.PhysicalCapacity
		safePct = loc.SafeCapacityPercentage
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return StoragePlanResult{}, err
	} else if grp, gerr := s.store.GetStorageGroup(ctx, in.FactoryID, in.ScopeCode); gerr == nil {
		id := grp.ID
		groupID = &id
		scopeCapacity = grp.PooledCapacity()
		for _, m := range grp.Members {
			if m.Status == domain.StatusActive && m.SafeCapacityPercentage < safePct {
				safePct = m.SafeCapacityPercentage
			}
		}
	} else {
		return StoragePlanResult{}, fmt.Errorf("%w: unknown storage scope %q", ErrNotFound, in.ScopeCode)
	}

	var productID, packagingID *int64
	if in.ProductCode != "" {
		p, err := s.productByCode(ctx, in.ProductCode)
		if err != nil {
			return StoragePlanResult{}, err
		}
		productID = &p.ID
	}
	if in.PackagingCode != "" {
		k, err := s.packagingByCode(ctx, in.PackagingCode)
		if err != nil {
			return StoragePlanResult{}, err
		}
		packagingID = &k.ID
	}

	// Carry the previous day's planned closing forward when no opening is
	// given, so a planner entering a run of days only supplies the movements.
	openingWeight, openingPackages := 0.0, 0.0
	if in.OpeningWeight != nil {
		openingWeight = *in.OpeningWeight
	}
	if in.OpeningPackages != nil {
		openingPackages = *in.OpeningPackages
	}
	if in.OpeningWeight == nil || in.OpeningPackages == nil {
		previous, err := s.store.ListDailyStoragePlans(ctx, postgres.StoragePlanFilter{
			FactoryID: in.FactoryID,
			ScopeCode: in.ScopeCode,
			From:      in.PlanDate.AddDate(0, 0, -1),
			To:        in.PlanDate.AddDate(0, 0, -1),
		})
		if err != nil {
			return StoragePlanResult{}, err
		}
		for _, p := range previous {
			if !sameOptional(p.ProductID, productID) || !sameOptional(p.PackagingTypeID, packagingID) {
				continue
			}
			if in.OpeningWeight == nil {
				openingWeight = p.PlannedClosingWeight
			}
			if in.OpeningPackages == nil {
				openingPackages = p.PlannedClosingPkgs
			}
			break
		}
	}

	closingWeight := openingWeight + in.PlannedInWeight - in.PlannedOutWeight
	closingPackages := openingPackages + in.PlannedInPackages - in.PlannedOutPackages

	uomID, err := s.store.UOMID(ctx, "TON")
	if err != nil {
		return StoragePlanResult{}, err
	}

	if err := s.store.SaveDailyStoragePlan(ctx, postgres.UpsertDailyStoragePlan{
		FactoryID:            in.FactoryID,
		PlanDate:             in.PlanDate,
		StorageLocationID:    locationID,
		StorageGroupID:       groupID,
		ProductID:            productID,
		PackagingTypeID:      packagingID,
		OpeningWeight:        openingWeight,
		PlannedInWeight:      in.PlannedInWeight,
		PlannedOutWeight:     in.PlannedOutWeight,
		PlannedClosingWeight: closingWeight,
		OpeningPackages:      openingPackages,
		PlannedInPackages:    in.PlannedInPackages,
		PlannedOutPackages:   in.PlannedOutPackages,
		PlannedClosingPkgs:   closingPackages,
		WeightUOMID:          uomID,
		Status:               in.Status,
		Remark:               in.Remark,
		UpdatedBy:            in.UpdatedBy,
	}); err != nil {
		return StoragePlanResult{}, err
	}

	// The plan is saved either way: planning above capacity is exactly the
	// situation the system exists to make visible, as the 2026/27 finished
	// sugar plan does. It warns rather than refuses.
	var warnings []Warning
	safeCapacity := scopeCapacity * safePct / 100
	if closingWeight > scopeCapacity+capacity.Epsilon {
		warnings = append(warnings, Warning{
			Field: "plannedClosingWeight",
			Message: fmt.Sprintf("planned closing of %.3f exceeds %s's capacity of %.3f",
				closingWeight, in.ScopeCode, scopeCapacity),
		})
	} else if closingWeight > safeCapacity+capacity.Epsilon {
		warnings = append(warnings, Warning{
			Field: "plannedClosingWeight",
			Message: fmt.Sprintf("planned closing of %.3f exceeds %s's safe level of %.3f",
				closingWeight, in.ScopeCode, safeCapacity),
		})
	}
	if closingWeight < -capacity.Epsilon {
		warnings = append(warnings, Warning{
			Field:   "plannedClosingWeight",
			Message: fmt.Sprintf("planned closing of %.3f is negative: more is planned out than in", closingWeight),
		})
	}

	return StoragePlanResult{
		PlanDate:       in.PlanDate.Format("2006-01-02"),
		ScopeCode:      in.ScopeCode,
		OpeningWeight:  capacity.Round(openingWeight, 3),
		PlannedIn:      capacity.Round(in.PlannedInWeight, 3),
		PlannedOut:     capacity.Round(in.PlannedOutWeight, 3),
		PlannedClosing: capacity.Round(closingWeight, 3),
		Capacity:       capacity.Round(scopeCapacity, 3),
		UtilizationPct: capacity.Round(capacity.Percentage(closingWeight, scopeCapacity), 2),
		Warnings:       warnings,
	}, nil
}

// SaveStoragePlan saves a run of plan lines, stopping at the first failure.
// Lines are applied in the order given, so a sequence that omits its opening
// figures carries forward correctly.
func (s *Service) SaveStoragePlan(ctx context.Context, lines []StoragePlanLineInput) ([]StoragePlanResult, error) {
	results := make([]StoragePlanResult, 0, len(lines))
	for i, line := range lines {
		res, err := s.SaveStoragePlanLine(ctx, line)
		if err != nil {
			return results, fmt.Errorf("line %d (%s %s): %w",
				i+1, line.ScopeCode, line.PlanDate.Format("2006-01-02"), err)
		}
		results = append(results, res)
	}
	return results, nil
}

func sameOptional(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
