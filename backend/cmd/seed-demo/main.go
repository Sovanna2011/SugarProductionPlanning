// Command seed-demo loads a realistic opening stock position so the dashboard
// has something to show.
//
// A fresh deployment has master data and the full season plan, but no stock,
// so every warehouse reads 0% and the remelt screen is empty. That makes the
// system hard to evaluate and hard to demonstrate.
//
// The positions are not invented: they are the season plan's own closing
// figures for a chosen date, so the dashboard shows what the factory expects
// to be holding that day. Pool figures are split across the pool's members in
// proportion to their capacity, because the plan does not split them.
//
// Stock is posted through the normal service, so it passes the same capacity
// validation as any other receipt. That is deliberate — if the demo data
// loads, the posting path demonstrably works.
//
//	go run ./cmd/seed-demo                    # 15 Feb 2027 by default
//	go run ./cmd/seed-demo -date 2027-04-10   # the raw sugar peak
//	go run ./cmd/seed-demo -force             # add to existing stock
//	go run ./cmd/seed-demo -if-empty          # a no-op when stock is already there
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/service"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
)

// split describes how a scope's planned closing stock is distributed into
// concrete product and packaging lines.
type split struct {
	scope     string
	product   string
	packaging string
	batch     string
	// share of the scope's planned closing weight, 0..1
	share float64
	// movement type used to bring it in
	movement string
}

// The demo position. Shares are a plausible product mix rather than anything
// the plan states, since the plan tracks pools by weight only.
var splits = []split{
	{"RAW-POOL", "RAW-SUGAR", "PKG-BULK", "RS26001", 0.80, "WAREHOUSE_RECEIPT"},
	{"RAW-POOL", "RAW-SUGAR", "PKG-JUMBO", "RJ26001", 0.20, "WAREHOUSE_RECEIPT"},

	{"FG-POOL", "REFINED-SUGAR", "PKG-50KG", "R26001", 0.40, "PACKING_RECEIPT"},
	{"FG-POOL", "WHITE-SUGAR", "PKG-50KG", "W26001", 0.45, "PACKING_RECEIPT"},
	{"FG-POOL", "REFINED-SUGAR", "PKG-JUMBO", "RJ26002", 0.15, "PACKING_RECEIPT"},

	{"CON-S01", "REFINED-SUGAR", "PKG-BULK", "", 1.00, "PRODUCTION_RECEIPT"},
}

// Molasses has no plan line in the workbook, so the tanks are filled to a
// stated fraction of capacity to give the dashboard something to show.
var molasses = []struct {
	storage string
	fill    float64
}{
	{"MOL-T01", 0.82},
	{"MOL-T02", 0.91},
	{"MOL-T03", 0.58},
}

func main() {
	var (
		dsn     = flag.String("database-url", os.Getenv("SPP_DATABASE_URL"), "PostgreSQL connection string")
		dateStr = flag.String("date", "2027-02-15", "plan date to take the position from (yyyy-mm-dd)")
		force   = flag.Bool("force", false, "load even when stock already exists")
		ifEmpty = flag.Bool("if-empty", false, "do nothing, successfully, when stock already exists")
	)
	flag.Parse()

	if *dsn == "" {
		log.Fatal("set SPP_DATABASE_URL or pass -database-url")
	}
	date, err := time.Parse("2006-01-02", *dateStr)
	if err != nil {
		log.Fatalf("bad -date: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	store, err := postgres.New(ctx, *dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer store.Close()

	factories, err := store.ListFactories(ctx)
	if err != nil || len(factories) == 0 {
		log.Fatalf("no factory found; run the migrations first: %v", err)
	}
	factoryID := factories[0].ID

	// Refuse to add to a database that already holds stock, so this can never
	// quietly inflate real balances.
	existing, err := store.ListBalances(ctx, postgres.BalanceFilter{
		FactoryID: factoryID, NonZeroOnly: true,
	})
	if err != nil {
		log.Fatalf("read balances: %v", err)
	}
	if len(existing) > 0 {
		// -if-empty is for an unattended start, where "the position is already
		// loaded" is the expected state on every restart after the first, and
		// exiting non-zero would stop the container.
		if *ifEmpty {
			fmt.Printf("%d stock lines already loaded; leaving them alone.\n", len(existing))
			return
		}
		if !*force {
			log.Fatalf("this database already holds %d stock lines; pass -force to add anyway", len(existing))
		}
	}

	svc := service.New(store)

	// The plan's own closing position for the chosen date.
	plans, err := store.ListDailyStoragePlans(ctx, postgres.StoragePlanFilter{
		FactoryID: factoryID, From: date, To: date,
	})
	if err != nil {
		log.Fatalf("read the plan: %v", err)
	}
	if len(plans) == 0 {
		log.Fatalf("no storage plan for %s; pick a date inside the season", date.Format("2006-01-02"))
	}
	closing := make(map[string]float64, len(plans))
	for _, p := range plans {
		closing[p.ScopeCode] += p.PlannedClosingWeight
	}

	locations, err := store.ListStorageLocations(ctx, postgres.StorageLocationFilter{FactoryID: factoryID})
	if err != nil {
		log.Fatalf("read locations: %v", err)
	}
	capacityOf := make(map[string]float64, len(locations))
	for _, l := range locations {
		capacityOf[l.StorageCode] = l.PhysicalCapacity
	}

	groups, err := store.ListStorageGroups(ctx, factoryID)
	if err != nil {
		log.Fatalf("read groups: %v", err)
	}
	membersOf := map[string][]string{}
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Status == "ACTIVE" {
				membersOf[g.GroupCode] = append(membersOf[g.GroupCode], m.StorageCode)
			}
		}
	}

	fmt.Printf("Loading the plan's position for %s\n\n", date.Format("2 Jan 2006"))

	// Packaged goods are received as whole bags, so a target weight is turned
	// into a package count and posted that way. Posting the weight directly
	// would leave fractional bags in the balance, and "18,217.391 spaces
	// available" is not a sentence anyone should read.
	packagings, err := store.ListPackagingTypes(ctx)
	if err != nil {
		log.Fatalf("read packaging: %v", err)
	}
	weightPerPackage := make(map[string]float64, len(packagings))
	for _, k := range packagings {
		if !k.IsBulk {
			weightPerPackage[k.Code] = k.WeightInTon
		}
	}

	var posted, failed int
	post := func(storage, product, packaging, batch, movement string, weight float64) {
		if weight <= 0.0005 {
			return
		}

		req := service.MovementRequest{
			FactoryID:     factoryID,
			MovementDate:  date,
			MovementType:  movement,
			StorageCode:   storage,
			ProductCode:   product,
			PackagingCode: packaging,
			BatchNo:       batch,
			ReferenceDoc:  "DEMO-" + date.Format("20060102"),
			Remark:        "Demo opening position from the season plan",
			PostedBy:      "seed-demo",
		}
		if per, ok := weightPerPackage[packaging]; ok && per > 0 {
			whole := math.Round(weight / per)
			if whole < 1 {
				return
			}
			req.PackageQuantity = &whole
		} else {
			req.WeightQuantity = &weight
		}

		res, err := svc.PostMovement(ctx, req)
		switch {
		case err != nil:
			failed++
			fmt.Printf("  %-9s %-14s %-10s  FAILED: %v\n", storage, product, packaging, err)
		case !res.Posted:
			failed++
			fmt.Printf("  %-9s %-14s %-10s  REFUSED:\n", storage, product, packaging)
			for _, f := range res.Validation.Findings {
				fmt.Printf("      %s: %s\n", f.Severity, f.Message)
			}
		default:
			posted++
			fmt.Printf("  %-9s %-14s %-10s %10.1f t", storage, product, packaging, res.Quantity.Weight)
			if res.Quantity.Packages > 0 {
				fmt.Printf("  (%.0f packages)", res.Quantity.Packages)
			}
			fmt.Println()
		}
	}

	for _, s := range splits {
		total := closing[s.scope] * s.share
		if total <= 0 {
			continue
		}

		// A pool is spread across its members in proportion to capacity; a
		// location takes the whole figure.
		targets := membersOf[s.scope]
		if len(targets) == 0 {
			post(s.scope, s.product, s.packaging, s.batch, s.movement, total)
			continue
		}
		var pooled float64
		for _, code := range targets {
			pooled += capacityOf[code]
		}
		for _, code := range targets {
			if pooled <= 0 {
				continue
			}
			post(code, s.product, s.packaging, s.batch, s.movement, total*capacityOf[code]/pooled)
		}
	}

	for _, m := range molasses {
		post(m.storage, "MOLASSES", "PKG-BULK", "", "TANK_RECEIPT", capacityOf[m.storage]*m.fill)
	}

	fmt.Printf("\n%d lines posted", posted)
	if failed > 0 {
		fmt.Printf(", %d failed", failed)
	}
	fmt.Println(".")

	if failed > 0 {
		os.Exit(1)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		os.Exit(1)
	}
}
