// Package service composes the store and the capacity engine into the
// operations the API exposes.
package service

import (
	"context"
	"sort"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// Service holds the dependencies shared by every operation.
type Service struct {
	store *postgres.Store
	// overrideRole, when set, is the role a user must hold to force a posting
	// that capacity validation blocked. Empty means no restriction.
	overrideRole string
	// authSessionTTL is how long a session survives without being used. Zero
	// means the default.
	authSessionTTL time.Duration
	// authSessionMaxLifetime caps how long use can keep a session alive.
	authSessionMaxLifetime time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithOverrideRole restricts capacity overrides to holders of a role
// (requirement section 24.5: "unless an authorized business rule explicitly
// permits an override"). Empty leaves overrides open to anyone, which is the
// default and the original behaviour.
func WithOverrideRole(role string) Option {
	return func(s *Service) { s.overrideRole = role }
}

// New builds a Service.
func New(store *postgres.Store, opts ...Option) *Service {
	s := &Service{store: store}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Store exposes the store for handlers that only need a straight read.
func (s *Service) Store() *postgres.Store { return s.store }

// ProductLine is one product + packaging line inside a storage location.
type ProductLine struct {
	ProductCode      string   `json:"productCode"`
	ProductName      string   `json:"productName"`
	PackagingCode    string   `json:"packagingCode"`
	PackagingDesc    string   `json:"packagingDescription"`
	WeightPerPackage float64  `json:"weightPerPackage"`
	CurrentPackages  float64  `json:"currentPackages"`
	CurrentWeight    float64  `json:"currentWeight"`
	ReservedWeight   float64  `json:"reservedWeight"`
	AvailableWeight  float64  `json:"availableWeight"`
	MaxPackages      *float64 `json:"maximumPackages,omitempty"`
	MaxWeight        *float64 `json:"maximumWeight,omitempty"`
	// AvailablePackageSpaces is the room left on the package axis — the
	// "15,000 spaces available" figure the requirement asks alerts to report.
	AvailablePackageSpaces *float64               `json:"availablePackageSpaces,omitempty"`
	AvailableWeightSpace   *float64               `json:"availableWeightCapacity,omitempty"`
	UtilizationPct         float64                `json:"utilizationPercentage"`
	Band                   capacity.ThresholdBand `json:"band"`
	Remark                 string                 `json:"remark"`
}

// StorageCard is the dashboard tile for one tank, silo or warehouse.
type StorageCard struct {
	StorageCode     string `json:"storageCode"`
	StorageName     string `json:"storageName"`
	StorageTypeCode string `json:"storageTypeCode"`
	StorageTypeName string `json:"storageTypeName"`
	TracksPackages  bool   `json:"tracksPackages"`
	Status          string `json:"status"`
	Remark          string `json:"remark"`
	// HasPlan reports whether a daily storage plan exists for this scope, and
	// therefore whether a forward projection can be produced for it.
	HasPlan     bool                 `json:"hasPlan"`
	Utilization capacity.Utilization `json:"utilization"`
	Products    []ProductLine        `json:"products"`
}

// GroupCard is the dashboard tile for a pooled group of locations.
type GroupCard struct {
	GroupCode   string               `json:"groupCode"`
	GroupName   string               `json:"groupName"`
	MemberCodes []string             `json:"memberCodes"`
	Utilization capacity.Utilization `json:"utilization"`
	Remark      string               `json:"remark"`
	HasPlan     bool                 `json:"hasPlan"`
}

// Alert is one capacity warning (requirement section 18).
type Alert struct {
	ScopeCode   string  `json:"scopeCode"`
	ScopeName   string  `json:"scopeName"`
	ScopeType   string  `json:"scopeType"`
	Severity    string  `json:"severity"`
	UIState     string  `json:"uiState"`
	Message     string  `json:"message"`
	Utilization float64 `json:"utilizationPercentage"`
}

// Dashboard is the payload behind the Storage Capacity Dashboard
// (requirement section 20).
type Dashboard struct {
	FactoryCode string        `json:"factoryCode"`
	FactoryName string        `json:"factoryName"`
	AsOfDate    string        `json:"asOfDate"`
	Groups      []GroupCard   `json:"groups"`
	Storages    []StorageCard `json:"storages"`
	Alerts      []Alert       `json:"alerts"`
}

// DashboardFilter narrows the dashboard.
type DashboardFilter struct {
	FactoryID       int64
	StorageTypeCode string
	StorageCode     string
	ProductCode     string
	PackagingCode   string
	Date            time.Time
}

// Dashboard builds the storage capacity picture for a factory.
func (s *Service) Dashboard(ctx context.Context, f DashboardFilter) (Dashboard, error) {
	date := f.Date
	if date.IsZero() {
		date = time.Now()
	}

	factories, err := s.store.ListFactories(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	out := Dashboard{
		AsOfDate: date.Format("2006-01-02"),
		Groups:   []GroupCard{},
		Storages: []StorageCard{},
		Alerts:   []Alert{},
	}
	for _, fac := range factories {
		if fac.ID == f.FactoryID || f.FactoryID == 0 {
			out.FactoryCode, out.FactoryName = fac.Code, fac.Name
			if f.FactoryID == 0 {
				f.FactoryID = fac.ID
			}
			break
		}
	}

	bands, err := s.store.ListThresholdBands(ctx, f.FactoryID)
	if err != nil {
		return Dashboard{}, err
	}
	locations, err := s.store.ListStorageLocations(ctx, postgres.StorageLocationFilter{
		FactoryID:       f.FactoryID,
		StorageTypeCode: f.StorageTypeCode,
		StorageCode:     f.StorageCode,
	})
	if err != nil {
		return Dashboard{}, err
	}
	totals, err := s.store.LocationTotalsByID(ctx, f.FactoryID)
	if err != nil {
		return Dashboard{}, err
	}
	balances, err := s.store.ListBalances(ctx, postgres.BalanceFilter{FactoryID: f.FactoryID})
	if err != nil {
		return Dashboard{}, err
	}
	capacities, err := s.store.ListStorageProductCapacity(ctx, postgres.CapacityFilter{
		FactoryID: f.FactoryID, OnDate: date,
	})
	if err != nil {
		return Dashboard{}, err
	}
	planned, err := s.store.PlannedScopeCodes(ctx, f.FactoryID)
	if err != nil {
		return Dashboard{}, err
	}

	// Index balances and capacities by location + product + packaging.
	type key struct {
		loc  int64
		prod int64
		pack int64
	}
	balanceByKey := map[key]domain.InventoryBalance{}
	for _, b := range balances {
		k := key{b.StorageLocationID, b.ProductID, b.PackagingTypeID}
		agg := balanceByKey[k]
		agg.PackageQty += b.PackageQty
		agg.WeightQty += b.WeightQty
		agg.ReservedWeight += b.ReservedWeight
		balanceByKey[k] = agg
	}

	for _, loc := range locations {
		total := totals[loc.ID]
		card := StorageCard{
			StorageCode:     loc.StorageCode,
			StorageName:     loc.StorageName,
			StorageTypeCode: loc.StorageTypeCode,
			StorageTypeName: loc.StorageTypeName,
			TracksPackages:  loc.TracksPackages,
			Status:          loc.Status,
			Remark:          loc.Remark,
			HasPlan:         planned[loc.StorageCode],
			Utilization: capacity.Utilize(total.TotalWeight, total.ReservedWeight,
				loc.PhysicalCapacity, loc.SafeCapacityPercentage, bands, loc.CapacityUOM),
		}

		for _, c := range capacities {
			if c.StorageLocationID != loc.ID {
				continue
			}
			if f.ProductCode != "" && c.ProductCode != f.ProductCode {
				continue
			}
			if f.PackagingCode != "" && c.PackagingCode != f.PackagingCode {
				continue
			}
			bal := balanceByKey[key{loc.ID, c.ProductID, c.PackagingTypeID}]

			line := ProductLine{
				ProductCode:      c.ProductCode,
				ProductName:      c.ProductName,
				PackagingCode:    c.PackagingCode,
				PackagingDesc:    c.PackagingDescription,
				WeightPerPackage: c.WeightPerPackage,
				CurrentPackages:  capacity.Round(bal.PackageQty, 3),
				CurrentWeight:    capacity.Round(bal.WeightQty, 3),
				ReservedWeight:   capacity.Round(bal.ReservedWeight, 3),
				AvailableWeight:  capacity.Round(bal.WeightQty-bal.ReservedWeight, 3),
				MaxPackages:      c.MaximumPackageQty,
				MaxWeight:        c.MaximumWeightQty,
				Remark:           c.Remark,
			}
			if c.MaximumPackageQty != nil && loc.TracksPackages {
				spaces := capacity.Round(*c.MaximumPackageQty-bal.PackageQty, 3)
				line.AvailablePackageSpaces = &spaces
			}
			if c.MaximumWeightQty != nil {
				space := capacity.Round(*c.MaximumWeightQty-bal.WeightQty, 3)
				line.AvailableWeightSpace = &space
				line.UtilizationPct = capacity.Round(capacity.Percentage(bal.WeightQty, *c.MaximumWeightQty), 2)
			}
			line.Band = capacity.Classify(bands, line.UtilizationPct)
			card.Products = append(card.Products, line)
		}

		out.Storages = append(out.Storages, card)
	}

	// Pooled groups.
	groups, err := s.store.ListStorageGroups(ctx, f.FactoryID)
	if err != nil {
		return Dashboard{}, err
	}
	for _, g := range groups {
		var current, reserved float64
		codes := make([]string, 0, len(g.Members))
		for _, m := range g.Members {
			current += totals[m.ID].TotalWeight
			reserved += totals[m.ID].ReservedWeight
			codes = append(codes, m.StorageCode)
		}
		pooled := g.PooledCapacity()
		// A pool inherits the tightest safe percentage of its members, so the
		// pool never looks safer than the strictest warehouse in it.
		safePct := 100.0
		for _, m := range g.Members {
			if m.Status == domain.StatusActive && m.SafeCapacityPercentage < safePct {
				safePct = m.SafeCapacityPercentage
			}
		}
		out.Groups = append(out.Groups, GroupCard{
			GroupCode:   g.GroupCode,
			GroupName:   g.GroupName,
			MemberCodes: codes,
			Remark:      g.Remark,
			HasPlan:     planned[g.GroupCode],
			Utilization: capacity.Utilize(current, reserved, pooled, safePct, bands, "TON"),
		})
	}

	out.Alerts = buildAlerts(out)
	return out, nil
}

// buildAlerts turns anything above the normal band into an operator message.
func buildAlerts(d Dashboard) []Alert {
	alerts := []Alert{}

	consider := func(code, name, scopeType string, u capacity.Utilization, msg string) {
		if u.Band.Severity == capacity.SeverityNormal {
			return
		}
		alerts = append(alerts, Alert{
			ScopeCode:   code,
			ScopeName:   name,
			ScopeType:   scopeType,
			Severity:    u.Band.Severity,
			UIState:     u.Band.UIState,
			Message:     msg,
			Utilization: u.UtilizationPct,
		})
	}

	for _, g := range d.Groups {
		consider(g.GroupCode, g.GroupName, "GROUP", g.Utilization,
			formatFull(g.GroupName, g.Utilization))
	}
	for _, st := range d.Storages {
		consider(st.StorageCode, st.StorageName, "LOCATION", st.Utilization,
			formatFull(st.StorageName, st.Utilization))

		for _, p := range st.Products {
			if p.Band.Severity == capacity.SeverityNormal {
				continue
			}
			msg := st.StorageName + " has only " +
				formatNumber(p.AvailableWeightSpace) + " " + st.Utilization.UOM +
				" of space left for " + p.ProductName + " " + p.PackagingCode
			if p.AvailablePackageSpaces != nil {
				msg = st.StorageName + " has only " +
					formatPackages(p.AvailablePackageSpaces) + " spaces available for " +
					p.ProductName + " " + p.PackagingCode
			}
			alerts = append(alerts, Alert{
				ScopeCode:   st.StorageCode,
				ScopeName:   st.StorageName + " / " + p.ProductName + " " + p.PackagingCode,
				ScopeType:   "PRODUCT",
				Severity:    p.Band.Severity,
				UIState:     p.Band.UIState,
				Message:     msg,
				Utilization: p.UtilizationPct,
			})
		}
	}

	// Most alarming first.
	rank := map[string]int{
		capacity.SeverityFull: 0, capacity.SeverityCritical: 1,
		capacity.SeverityHigh: 2, capacity.SeverityWarning: 3, capacity.SeverityNormal: 4,
	}
	sort.SliceStable(alerts, func(i, j int) bool {
		if rank[alerts[i].Severity] != rank[alerts[j].Severity] {
			return rank[alerts[i].Severity] < rank[alerts[j].Severity]
		}
		return alerts[i].Utilization > alerts[j].Utilization
	})
	return alerts
}
