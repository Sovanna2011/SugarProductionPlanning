package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// ProjectionRequest asks for the forward capacity curve of one storage scope.
type ProjectionRequest struct {
	FactoryID int64
	// ScopeCode is a storage location code or a storage group code.
	ScopeCode string
	// From defaults to the first planned day.
	From time.Time
	// Days caps the horizon; zero means the whole plan.
	Days int
	// UseActualOpening starts the curve from the posted balance rather than
	// the plan's own opening figure.
	UseActualOpening bool
}

// Project rolls the approved daily storage plan forward from an opening
// balance and reports when the scope is expected to breach its ceilings
// (requirement section 19).
func (s *Service) Project(ctx context.Context, req ProjectionRequest) (capacity.Projection, error) {
	bands, err := s.store.ListThresholdBands(ctx, req.FactoryID)
	if err != nil {
		return capacity.Projection{}, err
	}

	var (
		scopeName        string
		physicalCapacity float64
		safePct          float64
		locationIDs      []int64
	)

	if loc, err := s.store.GetStorageLocation(ctx, req.FactoryID, req.ScopeCode); err == nil {
		scopeName = loc.StorageName
		physicalCapacity = loc.PhysicalCapacity
		safePct = loc.SafeCapacityPercentage
		locationIDs = []int64{loc.ID}
	} else if grp, gerr := s.store.GetStorageGroup(ctx, req.FactoryID, req.ScopeCode); gerr == nil {
		scopeName = grp.GroupName
		physicalCapacity = grp.PooledCapacity()
		safePct = 100
		for _, m := range grp.Members {
			if m.Status == domain.StatusActive && m.SafeCapacityPercentage < safePct {
				safePct = m.SafeCapacityPercentage
			}
			locationIDs = append(locationIDs, m.ID)
		}
	} else {
		return capacity.Projection{}, fmt.Errorf("%w: unknown storage scope %q", ErrNotFound, req.ScopeCode)
	}

	plans, err := s.store.ListDailyStoragePlans(ctx, postgres.StoragePlanFilter{
		FactoryID: req.FactoryID,
		ScopeCode: req.ScopeCode,
		From:      req.From,
	})
	if err != nil {
		return capacity.Projection{}, err
	}
	if len(plans) == 0 {
		return capacity.Projection{}, fmt.Errorf("%w: no daily storage plan for scope %q",
			ErrNotFound, req.ScopeCode)
	}

	// Several plan lines may target the same scope on the same day (one per
	// product), so sum them into a single daily movement.
	type dayTotals struct {
		in, out float64
	}
	ordered := make([]time.Time, 0, len(plans))
	byDate := map[time.Time]*dayTotals{}
	for _, p := range plans {
		t, ok := byDate[p.PlanDate]
		if !ok {
			t = &dayTotals{}
			byDate[p.PlanDate] = t
			ordered = append(ordered, p.PlanDate)
		}
		t.in += p.PlannedInWeight
		t.out += p.PlannedOutWeight
	}

	if req.Days > 0 && len(ordered) > req.Days {
		ordered = ordered[:req.Days]
	}

	days := make([]capacity.PlanDay, 0, len(ordered))
	for _, d := range ordered {
		days = append(days, capacity.PlanDay{Date: d, In: byDate[d].in, Out: byDate[d].out})
	}

	opening := plans[0].OpeningWeight
	if req.UseActualOpening {
		opening = 0
		for _, id := range locationIDs {
			totals, err := s.store.LocationTotal(ctx, id)
			if err != nil {
				return capacity.Projection{}, err
			}
			opening += totals.TotalWeight
		}
	}

	from := req.From
	if from.IsZero() {
		from = ordered[0]
	}

	return capacity.Project(capacity.ProjectInput{
		ScopeCode:        req.ScopeCode,
		ScopeName:        scopeName,
		Opening:          opening,
		PhysicalCapacity: physicalCapacity,
		SafePercentage:   safePct,
		UOM:              "TON",
		Days:             days,
		Bands:            bands,
		From:             from,
	}), nil
}

// PlanVsActualRow compares one storage scope's plan with what was posted
// (requirement section 17).
type PlanVsActualRow struct {
	ScopeCode      string                 `json:"scopeCode"`
	ScopeName      string                 `json:"scopeName"`
	ProductCode    string                 `json:"productCode,omitempty"`
	PackagingCode  string                 `json:"packagingCode,omitempty"`
	Opening        float64                `json:"opening"`
	PlannedIn      float64                `json:"plannedIn"`
	ActualIn       float64                `json:"actualIn"`
	PlannedOut     float64                `json:"plannedOut"`
	ActualOut      float64                `json:"actualOut"`
	PlannedClosing float64                `json:"plannedClosing"`
	ActualClosing  float64                `json:"actualClosing"`
	Variance       float64                `json:"variance"`
	Capacity       float64                `json:"capacity"`
	AvailableCap   float64                `json:"availableCapacity"`
	UtilizationPct float64                `json:"utilizationPercentage"`
	Band           capacity.ThresholdBand `json:"band"`
	UOM            string                 `json:"uom"`
}

// PlanVsActual returns the daily comparison for every planned scope on a date.
//
// Actual closing stock is derived from posted movements, never keyed in
// (requirement section 16).
func (s *Service) PlanVsActual(ctx context.Context, factoryID int64, date time.Time) ([]PlanVsActualRow, error) {
	bands, err := s.store.ListThresholdBands(ctx, factoryID)
	if err != nil {
		return nil, err
	}
	plans, err := s.store.ListDailyStoragePlans(ctx, postgres.StoragePlanFilter{
		FactoryID: factoryID, From: date, To: date,
	})
	if err != nil {
		return nil, err
	}
	actualsIn, err := s.store.ActualsForLocations(ctx, factoryID, date)
	if err != nil {
		return nil, err
	}
	closing, err := s.store.BalanceAsOf(ctx, factoryID, date)
	if err != nil {
		return nil, err
	}
	locations, err := s.store.ListStorageLocations(ctx, postgres.StorageLocationFilter{FactoryID: factoryID})
	if err != nil {
		return nil, err
	}
	groups, err := s.store.ListStorageGroups(ctx, factoryID)
	if err != nil {
		return nil, err
	}

	// Resolve a scope code to the locations whose actuals roll up into it.
	membersOf := map[string][]int64{}
	capacityOf := map[string]float64{}
	safeOf := map[string]float64{}
	for _, l := range locations {
		membersOf[l.StorageCode] = []int64{l.ID}
		capacityOf[l.StorageCode] = l.PhysicalCapacity
		safeOf[l.StorageCode] = l.SafeCapacityPercentage
	}
	for _, g := range groups {
		var ids []int64
		safe := 100.0
		for _, m := range g.Members {
			ids = append(ids, m.ID)
			if m.Status == domain.StatusActive && m.SafeCapacityPercentage < safe {
				safe = m.SafeCapacityPercentage
			}
		}
		membersOf[g.GroupCode] = ids
		capacityOf[g.GroupCode] = g.PooledCapacity()
		safeOf[g.GroupCode] = safe
	}

	rows := make([]PlanVsActualRow, 0, len(plans))
	for _, p := range plans {
		var actIn, actOut, actClosing float64
		for _, id := range membersOf[p.ScopeCode] {
			actIn += actualsIn[id].InWeight
			actOut += actualsIn[id].OutWeight
			actClosing += closing[id]
		}
		physical := capacityOf[p.ScopeCode]
		u := capacity.Utilize(actClosing, 0, physical, safeOf[p.ScopeCode], bands, p.WeightUOM)

		rows = append(rows, PlanVsActualRow{
			ScopeCode:      p.ScopeCode,
			ScopeName:      p.ScopeName,
			ProductCode:    p.ProductCode,
			PackagingCode:  p.PackagingCode,
			Opening:        p.OpeningWeight,
			PlannedIn:      p.PlannedInWeight,
			ActualIn:       capacity.Round(actIn, 3),
			PlannedOut:     p.PlannedOutWeight,
			ActualOut:      capacity.Round(actOut, 3),
			PlannedClosing: p.PlannedClosingWeight,
			ActualClosing:  capacity.Round(actClosing, 3),
			Variance:       capacity.Round(actClosing-p.PlannedClosingWeight, 3),
			Capacity:       u.Capacity,
			AvailableCap:   u.AvailableCapacity,
			UtilizationPct: u.UtilizationPct,
			Band:           u.Band,
			UOM:            p.WeightUOM,
		})
	}
	return rows, nil
}

// formatNumber renders an optional weight for an alert message.
func formatNumber(v *float64) string {
	if v == nil {
		return "0"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// formatPackages renders a package count for an alert message. Packages are
// whole things, so "18,217.391 spaces available" is never the right sentence
// even when the underlying balance carries a fraction.
func formatPackages(v *float64) string {
	if v == nil {
		return "0"
	}
	return strconv.FormatFloat(math.Round(*v), 'f', 0, 64)
}

func formatFull(name string, u capacity.Utilization) string {
	return fmt.Sprintf("%s is %s%% full (%s of %s %s)", name,
		strconv.FormatFloat(u.UtilizationPct, 'f', -1, 64),
		strconv.FormatFloat(u.CurrentStock, 'f', -1, 64),
		strconv.FormatFloat(u.Capacity, 'f', -1, 64), u.UOM)
}
