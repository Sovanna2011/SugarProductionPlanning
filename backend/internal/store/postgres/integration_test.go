//go:build integration

// Integration tests that run against a real PostgreSQL instance.
//
//	createdb spp_test
//	SPP_TEST_DATABASE_URL=postgres://localhost/spp_test go test -tags=integration ./...
//
// They are build-tagged so the default `go test ./...` stays hermetic. What
// they cover that the unit tests cannot: the SQL itself, migration
// idempotency, the seeded season reconciling with the summary report, and the
// service layer wiring the store to the capacity engine.
package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
	"github.com/sovanna2011/sugarproductionplanning/backend/migrations"
)

func open(t *testing.T) (*postgres.Store, context.Context) {
	t.Helper()

	dsn := os.Getenv("SPP_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("SPP_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set SPP_TEST_DATABASE_URL to run the integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	store, err := postgres.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(store.Close)

	files, err := migrations.Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(ctx, files); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store, ctx
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestMigrationsAreIdempotent re-applies every migration and expects a no-op.
// A migration that is not guarded by schema_migrations would fail here on the
// duplicate seed inserts.
func TestMigrationsAreIdempotent(t *testing.T) {
	store, ctx := open(t)

	files, err := migrations.Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(ctx, files); err != nil {
		t.Fatalf("second migrate should be a no-op: %v", err)
	}

	locations, err := store.ListStorageLocations(ctx, postgres.StorageLocationFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("list locations: %v", err)
	}
	if len(locations) != 9 {
		t.Errorf("storage locations = %d, want 9 (3 tanks, 2 raw, 1 silo, 3 finished)", len(locations))
	}
}

// TestSeededMasterData checks the storage structure the requirement specifies.
func TestSeededMasterData(t *testing.T) {
	store, ctx := open(t)

	byType := map[string]int{}
	locations, err := store.ListStorageLocations(ctx, postgres.StorageLocationFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("list locations: %v", err)
	}
	for _, l := range locations {
		byType[l.StorageTypeCode]++
	}
	want := map[string]int{
		"TANK":                     3,
		"RAW_SUGAR_WAREHOUSE":      2,
		"CONDITIONING_SILO":        1,
		"FINISHED_GOODS_WAREHOUSE": 3,
	}
	for code, n := range want {
		if byType[code] != n {
			t.Errorf("%s locations = %d, want %d", code, byType[code], n)
		}
	}

	// The jumbo bag weight drives every bag/ton conversion in the system.
	packagings, err := store.ListPackagingTypes(ctx)
	if err != nil {
		t.Fatalf("list packaging: %v", err)
	}
	found := false
	for _, p := range packagings {
		if p.Code == "PKG-JUMBO" {
			found = true
			if p.WeightInTon != 1.1 {
				t.Errorf("jumbo bag = %v t, want 1.1", p.WeightInTon)
			}
		}
	}
	if !found {
		t.Error("PKG-JUMBO missing from the packaging master")
	}

	// The pooled capacities the 2026/27 plan is built on.
	groups, err := store.ListStorageGroups(ctx, 0)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	pooled := map[string]float64{}
	for _, g := range groups {
		pooled[g.GroupCode] = g.PooledCapacity()
	}
	if pooled["RAW-POOL"] != 110_000 {
		t.Errorf("raw sugar pool = %v t, want 110,000", pooled["RAW-POOL"])
	}
	if pooled["FG-POOL"] != 69_000 {
		t.Errorf("finished sugar pool = %v t, want 69,000", pooled["FG-POOL"])
	}
}

// TestSeededSeasonReconciles asserts the seeded plan still adds up to the
// figures in the season summary report.
func TestSeededSeasonReconciles(t *testing.T) {
	store, ctx := open(t)

	plans, err := store.ListDailyProductionPlans(ctx, postgres.ProductionPlanFilter{})
	if err != nil {
		t.Fatalf("list production plans: %v", err)
	}
	if len(plans) != 276 {
		t.Fatalf("planned days = %d, want 276 (01 Dec 2026 .. 02 Sep 2027)", len(plans))
	}

	var cane, raw, remelt, refined, white, superRefined, quota, packing float64
	for _, p := range plans {
		cane += p.CaneTarget
		raw += p.RawSugarTarget
		remelt += p.RawToRemeltTarget
		refined += p.RefinedSugarTarget
		white += p.WhiteSugarTarget
		superRefined += p.SuperRefinedTarget
		quota += p.QuotaSalesTarget
		packing += p.RawPackingTarget
	}

	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"cane crushed", cane, 2_300_000},
		{"raw sugar produced", raw, 253_000},
		{"raw direct to remelt", remelt, 124_950},
		{"refined sugar", refined, 106_700},
		{"white sugar", white, 133_400},
		{"super refined sugar", superRefined, 2_000},
		{"quota sales", quota, 136_000},
		{"raw sugar jumbo bagging", packing, 20_700},
	} {
		if diff := c.got - c.want; diff > 0.5 || diff < -0.5 {
			t.Errorf("%s = %.1f t, want %.1f t", c.name, c.got, c.want)
		}
	}
}

// TestStoragePlanBalances checks every seeded plan line obeys
// closing = opening + in - out.
func TestStoragePlanBalances(t *testing.T) {
	store, ctx := open(t)

	lines, err := store.ListDailyStoragePlans(ctx, postgres.StoragePlanFilter{})
	if err != nil {
		t.Fatalf("list storage plans: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("no daily storage plan lines were seeded")
	}
	for _, l := range lines {
		want := l.OpeningWeight + l.PlannedInWeight - l.PlannedOutWeight
		if diff := l.PlannedClosingWeight - want; diff > 0.001 || diff < -0.001 {
			t.Fatalf("%s on %s: closing %.3f, but opening %.3f + in %.3f - out %.3f = %.3f",
				l.ScopeCode, l.PlanDate.Format("2006-01-02"),
				l.PlannedClosingWeight, l.OpeningWeight, l.PlannedInWeight, l.PlannedOutWeight, want)
		}
	}
}

// TestProjectionReproducesTheSeasonFindings is the end-to-end check that
// matters most: the store, the plan and the capacity engine together must
// still produce the dates in the season summary.
func TestProjectionReproducesTheSeasonFindings(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	t.Run("finished sugar pool overflows on 28 May 2027", func(t *testing.T) {
		p, err := svc.Project(ctx, service.ProjectionRequest{ScopeCode: "FG-POOL"})
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		if p.FirstPhysicalBreach == nil {
			t.Fatal("expected the finished sugar pool to overflow")
		}
		if got := p.FirstPhysicalBreach.Format("2006-01-02"); got != "2027-05-28" {
			t.Errorf("first overflow = %s, want 2027-05-28", got)
		}
		if p.Capacity != 69_000 {
			t.Errorf("pool capacity = %v, want 69,000", p.Capacity)
		}
	})

	t.Run("raw sugar pool peaks at 109,720 t within capacity", func(t *testing.T) {
		p, err := svc.Project(ctx, service.ProjectionRequest{ScopeCode: "RAW-POOL"})
		if err != nil {
			t.Fatalf("project: %v", err)
		}
		if diff := p.PeakClosing - 109_720; diff > 0.5 || diff < -0.5 {
			t.Errorf("raw sugar peak = %.1f t, want 109,720", p.PeakClosing)
		}
		if p.PeakDate == nil || p.PeakDate.Format("2006-01-02") != "2027-04-10" {
			t.Errorf("raw sugar peak date = %v, want 2027-04-10", p.PeakDate)
		}
		if p.FirstPhysicalBreach != nil {
			t.Errorf("raw sugar should stay within 110,000 t, but breaches on %v", p.FirstPhysicalBreach)
		}
	})

	t.Run("a scope with no plan is not found", func(t *testing.T) {
		// FG-WH01 is planned as part of FG-POOL, not on its own.
		if _, err := svc.Project(ctx, service.ProjectionRequest{ScopeCode: "FG-WH01"}); err == nil {
			t.Fatal("expected an error for a scope with no daily plan")
		}
	})
}

// TestCapacityValidationAgainstTheDatabase drives the pre-posting rules
// through the service, so the capacity matrix, the balance queries and the
// engine are all exercised together.
func TestCapacityValidationAgainstTheDatabase(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	factories, err := store.ListFactories(ctx)
	if err != nil || len(factories) == 0 {
		t.Fatalf("no factory seeded: %v", err)
	}
	factoryID := factories[0].ID

	// The conditioning silo is a clean subject: 2,000 t of capacity, and bulk
	// refined sugar is the only thing configured to go in it.
	t.Run("a receipt within every ceiling is allowed", func(t *testing.T) {
		res, err := svc.ValidateMovement(ctx, service.MovementRequest{
			FactoryID:      factoryID,
			MovementType:   "PRODUCTION_RECEIPT",
			StorageCode:    "CON-S01",
			ProductCode:    "REFINED-SUGAR",
			PackagingCode:  "PKG-BULK",
			WeightQuantity: f(100),
		})
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if !res.Validation.Allowed {
			t.Errorf("expected allowed, got %+v", res.Validation.Findings)
		}
	})

	t.Run("a receipt beyond physical capacity is blocked", func(t *testing.T) {
		res, err := svc.ValidateMovement(ctx, service.MovementRequest{
			FactoryID:      factoryID,
			MovementType:   "PRODUCTION_RECEIPT",
			StorageCode:    "CON-S01",
			ProductCode:    "REFINED-SUGAR",
			PackagingCode:  "PKG-BULK",
			WeightQuantity: f(5_000),
		})
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if res.Validation.Allowed {
			t.Fatal("5,000 t into a 2,000 t silo must be blocked")
		}
		if !res.Validation.RequiresOverride {
			t.Error("a blocked posting should be flagged as override-able")
		}
	})

	t.Run("an unconfigured product is blocked", func(t *testing.T) {
		res, err := svc.ValidateMovement(ctx, service.MovementRequest{
			FactoryID:       factoryID,
			MovementType:    "WAREHOUSE_RECEIPT",
			StorageCode:     "RAW-WH01",
			ProductCode:     "REFINED-SUGAR",
			PackagingCode:   "PKG-50KG",
			PackageQuantity: f(10),
		})
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if res.Validation.Allowed {
			t.Fatal("refined sugar is not configured for a raw sugar warehouse")
		}
	})

	t.Run("packages convert to weight from the packaging master", func(t *testing.T) {
		res, err := svc.ValidateMovement(ctx, service.MovementRequest{
			FactoryID:       factoryID,
			MovementType:    "PACKING_RECEIPT",
			StorageCode:     "FG-WH01",
			ProductCode:     "REFINED-SUGAR",
			PackagingCode:   "PKG-50KG",
			PackageQuantity: f(100_000),
		})
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		// 100,000 bags x 50 KG = 5,000 t.
		if diff := res.Quantity.Weight - 5_000; diff > capacity.Epsilon || diff < -capacity.Epsilon {
			t.Errorf("derived weight = %v t, want 5,000", res.Quantity.Weight)
		}
	})
}

// TestPostingUpdatesTheDerivedBalance posts a movement and checks the balance
// followed it, then reverses the movement so the test leaves no residue.
func TestPostingUpdatesTheDerivedBalance(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	factories, err := store.ListFactories(ctx)
	if err != nil || len(factories) == 0 {
		t.Fatalf("no factory seeded: %v", err)
	}
	factoryID := factories[0].ID

	const batch = "IT-BATCH-001"
	post := func(movementType string, packages float64) service.MovementResponse {
		t.Helper()
		res, err := svc.PostMovement(ctx, service.MovementRequest{
			FactoryID:       factoryID,
			MovementDate:    date(2027, time.January, 15),
			MovementType:    movementType,
			StorageCode:     "FG-WH01",
			ProductCode:     "REFINED-SUGAR",
			PackagingCode:   "PKG-50KG",
			BatchNo:         batch,
			PackageQuantity: f(packages),
			PostedBy:        "integration-test",
		})
		if err != nil {
			t.Fatalf("%s: %v", movementType, err)
		}
		if !res.Posted {
			t.Fatalf("%s was not posted: %+v", movementType, res.Validation.Findings)
		}
		return res
	}

	balance := func() (packages, weight float64) {
		t.Helper()
		rows, err := store.ListBalances(ctx, postgres.BalanceFilter{
			FactoryID: factoryID, StorageCode: "FG-WH01", BatchNo: batch,
		})
		if err != nil {
			t.Fatalf("list balances: %v", err)
		}
		for _, r := range rows {
			packages += r.PackageQty
			weight += r.WeightQty
		}
		return
	}

	startPackages, startWeight := balance()

	post("PACKING_RECEIPT", 20_000) // 1,000 t
	gotPackages, gotWeight := balance()
	if gotPackages-startPackages != 20_000 {
		t.Errorf("packages moved by %v, want 20,000", gotPackages-startPackages)
	}
	if diff := (gotWeight - startWeight) - 1_000; diff > capacity.Epsilon || diff < -capacity.Epsilon {
		t.Errorf("weight moved by %v t, want 1,000", gotWeight-startWeight)
	}

	// An issue takes it back out, leaving the database as it was found.
	post("SALES_ISSUE", 20_000)
	endPackages, endWeight := balance()
	if endPackages != startPackages {
		t.Errorf("packages after reversal = %v, want %v", endPackages, startPackages)
	}
	if diff := endWeight - startWeight; diff > capacity.Epsilon || diff < -capacity.Epsilon {
		t.Errorf("weight after reversal = %v, want %v", endWeight, startWeight)
	}
}

// TestDashboardBuilds exercises the whole read path behind the UI5 dashboard.
func TestDashboardBuilds(t *testing.T) {
	store, ctx := open(t)
	svc := service.New(store)

	d, err := svc.Dashboard(ctx, service.DashboardFilter{})
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if d.FactoryCode == "" {
		t.Error("dashboard has no factory")
	}
	if len(d.Storages) == 0 {
		t.Error("dashboard has no storage cards")
	}
	if len(d.Groups) != 2 {
		t.Errorf("dashboard group cards = %d, want 2", len(d.Groups))
	}
	for _, g := range d.Groups {
		if !g.HasPlan {
			t.Errorf("pooled group %s should carry a daily plan", g.GroupCode)
		}
	}

	// Every card must classify into a configured alert band.
	for _, s := range d.Storages {
		if s.Utilization.Band.Code == "" {
			t.Errorf("%s has no threshold band", s.StorageCode)
		}
	}
}

func f(v float64) *float64 { return &v }
