package capacity

import (
	"math"
	"testing"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

func almost(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-3 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// --- conversion (requirement section 14) -----------------------------------

func TestWeightOfUsesConfiguredPackageWeight(t *testing.T) {
	// 100,000 bags x 50 KG = 5,000 tons.
	almost(t, WeightOf(100_000, 0.050), 5_000, "refined 50 KG")
	// 5,000 jumbo bags x 1.1 t = 5,500 tons.
	almost(t, WeightOf(5_000, 1.100), 5_500, "jumbo")
}

func TestPackagesOfIsTheInverse(t *testing.T) {
	almost(t, PackagesOf(5_000, 0.050), 100_000, "tons to 50 KG bags")
	almost(t, PackagesOf(5_500, 1.100), 5_000, "tons to jumbo bags")
}

func TestPackagesOfBulkHasNoPackageCount(t *testing.T) {
	if got := PackagesOf(1_500, 0); got != 0 {
		t.Errorf("bulk package count = %v, want 0", got)
	}
	q := FromWeight(1_500, 0)
	if q.Packages != 0 || q.Weight != 1_500 {
		t.Errorf("FromWeight(bulk) = %+v, want {0 1500}", q)
	}
}

func TestPercentageGuardsZeroCapacity(t *testing.T) {
	almost(t, Percentage(0, 0), 0, "nothing in nothing")
	almost(t, Percentage(10, 0), 100, "something in nothing")
	almost(t, Percentage(4_000, 5_000), 80, "tank 1 of the requirement example")
}

// --- utilisation and alert bands (requirement sections 7, 17, 18) ----------

func TestUtilizeMatchesTheTankExample(t *testing.T) {
	// Requirement section 7: 5,000 t physical, 4,750 t safe (95%),
	// 4,000 t on hand -> 80% utilisation, 750 t available.
	u := Utilize(4_000, 0, 5_000, 95, DefaultBands, "TON")
	almost(t, u.SafeCapacity, 4_750, "safe capacity")
	almost(t, u.UtilizationPct, 80, "utilisation")
	almost(t, u.AvailableCapacity, 750, "available to safe")
	almost(t, u.AvailablePhysical, 1_000, "available to physical")
	if u.Band.Severity != SeverityWarning {
		t.Errorf("band at 80%% = %s, want WARNING", u.Band.Severity)
	}
}

func TestUtilizeReportsAvailableStockNetOfReservations(t *testing.T) {
	u := Utilize(20_000, 2_000, 45_000, 95, DefaultBands, "TON")
	almost(t, u.AvailableStock, 18_000, "available stock")
}

func TestClassifyBandBoundaries(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, SeverityNormal},
		{79.99, SeverityNormal},
		{80, SeverityWarning},
		{89.99, SeverityWarning},
		{90, SeverityHigh},
		{94.999, SeverityHigh},
		{95, SeverityCritical},
		{99.99, SeverityCritical},
		{100, SeverityFull},
		{140, SeverityFull},
	}
	for _, c := range cases {
		if got := Classify(DefaultBands, c.pct).Severity; got != c.want {
			t.Errorf("Classify(%v) = %s, want %s", c.pct, got, c.want)
		}
	}
}

func TestClassifyAcceptsUnsortedAndEmptyBands(t *testing.T) {
	shuffled := []ThresholdBand{DefaultBands[3], DefaultBands[0], DefaultBands[4], DefaultBands[1], DefaultBands[2]}
	if got := Classify(shuffled, 92).Severity; got != SeverityHigh {
		t.Errorf("unsorted bands gave %s, want HIGH", got)
	}
	if got := Classify(nil, 92).Severity; got != SeverityHigh {
		t.Errorf("nil bands gave %s, want HIGH from the defaults", got)
	}
}

// --- receipt validation (requirement section 24) ---------------------------

func warehouse() domain.StorageLocation {
	return domain.StorageLocation{
		ID:                     1,
		StorageCode:            "FG-WH01",
		StorageName:            "Finished Sugar Warehouse 1",
		StorageTypeCode:        domain.StorageTypeFinishedGoodsWarehouse,
		TracksPackages:         true,
		PhysicalCapacity:       22_000,
		CapacityUOM:            "TON",
		SafeCapacityPercentage: 95,
		AllowMixedProducts:     true,
		AllowMixedBatches:      true,
		Status:                 domain.StatusActive,
	}
}

func refined50kgCapacity() *domain.StorageProductCapacity {
	maxPkg, maxWt := 200_000.0, 10_000.0
	return &domain.StorageProductCapacity{
		StorageLocationID: 1,
		ProductID:         10,
		ProductCode:       "REFINED-SUGAR",
		PackagingTypeID:   3,
		PackagingCode:     "PKG-50KG",
		WeightPerPackage:  0.050,
		MaximumPackageQty: &maxPkg,
		MaximumWeightQty:  &maxWt,
		WeightUOM:         "TON",
		Status:            domain.StatusActive,
	}
}

func receipt(packages float64) Receipt {
	return Receipt{
		ProductID:       10,
		ProductName:     "Refined Sugar",
		PackagingTypeID: 3,
		PackagingCode:   "PKG-50KG",
		Quantity:        FromPackages(packages, 0.050),
	}
}

func TestValidateAllowsAReceiptThatFits(t *testing.T) {
	loc := LocationState{Location: warehouse(), TotalWeight: 5_000, ProductIDsOnHand: []int64{10}}
	prod := ProductState{Capacity: refined50kgCapacity(), Current: FromPackages(100_000, 0.050)}

	res := Validate(loc, prod, receipt(20_000), DefaultBands)
	if !res.Allowed {
		t.Fatalf("expected the receipt to be allowed, got findings %+v", res.Findings)
	}
	if len(res.Blocking()) != 0 {
		t.Errorf("unexpected blocking findings: %+v", res.Blocking())
	}
}

func TestValidateBlocksOnProductWeightCeiling(t *testing.T) {
	// 190,000 bags on hand (9,500 t) plus 20,000 bags (1,000 t) = 10,500 t
	// against a 10,000 t product limit.
	loc := LocationState{Location: warehouse(), TotalWeight: 9_500, ProductIDsOnHand: []int64{10}}
	prod := ProductState{Capacity: refined50kgCapacity(), Current: FromPackages(190_000, 0.050)}

	res := Validate(loc, prod, receipt(20_000), DefaultBands)
	if res.Allowed {
		t.Fatal("expected the product ceiling to block the receipt")
	}
	var found bool
	for _, f := range res.Blocking() {
		if f.Check == CheckProductCapacity {
			found = true
			almost(t, f.Excess, 500, "product capacity excess")
		}
	}
	if !found {
		t.Errorf("no PRODUCT_CAPACITY finding in %+v", res.Findings)
	}
}

func TestValidateBlocksOnPackageCeiling(t *testing.T) {
	// A package ceiling can bite before the weight ceiling does; here we lift
	// the weight limit so only the 200,000 bag limit applies.
	cap := refined50kgCapacity()
	big := 1_000_000.0
	cap.MaximumWeightQty = &big

	loc := LocationState{Location: warehouse(), TotalWeight: 9_500, ProductIDsOnHand: []int64{10}}
	prod := ProductState{Capacity: cap, Current: FromPackages(190_000, 0.050)}

	res := Validate(loc, prod, receipt(20_000), DefaultBands)
	if res.Allowed {
		t.Fatal("expected the package ceiling to block the receipt")
	}
	for _, f := range res.Blocking() {
		if f.Check == CheckPackageCapacity {
			almost(t, f.Excess, 10_000, "package excess")
			return
		}
	}
	t.Errorf("no PACKAGE_CAPACITY finding in %+v", res.Findings)
}

func TestValidateBlocksOnPhysicalCapacityEvenWhenTheProductFits(t *testing.T) {
	// This is requirement section 13: the warehouse already holds 21,500 t of
	// other products. The refined 50 KG line has plenty of headroom, but the
	// building does not.
	loc := LocationState{Location: warehouse(), TotalWeight: 21_500, ProductIDsOnHand: []int64{10, 11}}
	prod := ProductState{Capacity: refined50kgCapacity(), Current: FromPackages(10_000, 0.050)}

	res := Validate(loc, prod, receipt(20_000), DefaultBands) // 1,000 t
	if res.Allowed {
		t.Fatal("expected the physical ceiling to block the receipt")
	}
	for _, f := range res.Blocking() {
		if f.Check == CheckPhysicalCapacity {
			almost(t, f.Projected, 22_500, "projected total")
			almost(t, f.Excess, 500, "physical excess")
			return
		}
	}
	t.Errorf("no PHYSICAL_CAPACITY finding in %+v", res.Findings)
}

func TestValidateWarnsButAllowsBetweenSafeAndPhysical(t *testing.T) {
	// 21,000 t on hand + 500 t = 21,500 t. Safe is 20,900 t (95% of 22,000),
	// physical is 22,000 t: a warning, not a block.
	loc := LocationState{Location: warehouse(), TotalWeight: 21_000, ProductIDsOnHand: []int64{10}}
	prod := ProductState{Capacity: refined50kgCapacity(), Current: FromPackages(10_000, 0.050)}

	res := Validate(loc, prod, receipt(10_000), DefaultBands) // 500 t
	if !res.Allowed {
		t.Fatalf("expected a warning, not a block: %+v", res.Blocking())
	}
	for _, f := range res.Findings {
		if f.Check == CheckSafeCapacity && f.Severity == FindingWarning {
			return
		}
	}
	t.Errorf("no SAFE_CAPACITY warning in %+v", res.Findings)
}

func TestValidateBlocksWhenNoCapacityIsConfigured(t *testing.T) {
	loc := LocationState{Location: warehouse(), TotalWeight: 0}
	res := Validate(loc, ProductState{Capacity: nil}, receipt(1_000), DefaultBands)
	if res.Allowed {
		t.Fatal("an unconfigured product must not be accepted into a location")
	}
	if !res.RequiresOverride {
		t.Error("a blocked posting should be flagged as override-able")
	}
}

func TestValidateBlocksMixedProductsWhenNotAllowed(t *testing.T) {
	loc := LocationState{Location: warehouse(), TotalWeight: 100, ProductIDsOnHand: []int64{11}}
	loc.Location.AllowMixedProducts = false
	prod := ProductState{Capacity: refined50kgCapacity(), Current: Quantity{}}

	res := Validate(loc, prod, receipt(100), DefaultBands)
	for _, f := range res.Blocking() {
		if f.Check == CheckMixedProducts {
			return
		}
	}
	t.Errorf("no MIXED_PRODUCTS finding in %+v", res.Findings)
}

func TestValidateIgnoresWithdrawals(t *testing.T) {
	loc := LocationState{Location: warehouse(), TotalWeight: 21_999}
	prod := ProductState{Capacity: nil} // would otherwise block
	res := Validate(loc, prod, Receipt{Quantity: Quantity{Packages: -100, Weight: -5}}, DefaultBands)
	if !res.Allowed || len(res.Findings) != 0 {
		t.Errorf("a withdrawal should not be capacity checked, got %+v", res)
	}
}

func TestValidateBlocksOnAnInactiveLocation(t *testing.T) {
	loc := LocationState{Location: warehouse()}
	loc.Location.Status = domain.StatusInactive
	res := Validate(loc, ProductState{Capacity: refined50kgCapacity()}, receipt(10), DefaultBands)
	if res.Allowed {
		t.Fatal("an inactive location must not accept stock")
	}
}

// --- projection (requirement section 19) -----------------------------------

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestProjectRollsTheBalanceForward(t *testing.T) {
	// Requirement section 19: 80% full today, +1,000 t production and -200 t
	// sale tomorrow -> 84%.
	in := ProjectInput{
		ScopeCode:        "FG-WH01",
		Opening:          8_000,
		PhysicalCapacity: 10_000,
		SafePercentage:   95,
		From:             day(2026, time.December, 1),
		Days: []PlanDay{
			{Date: day(2026, time.December, 1), In: 0, Out: 0},
			{Date: day(2026, time.December, 2), In: 1_000, Out: 200},
		},
		Bands: DefaultBands,
	}
	p := Project(in)
	almost(t, p.Days[0].UtilizationPct, 80, "today")
	almost(t, p.Days[1].Closing, 8_800, "tomorrow closing")
	almost(t, p.Days[1].UtilizationPct, 88, "tomorrow utilisation")
}

func TestProjectFindsTheFirstBreachDates(t *testing.T) {
	in := ProjectInput{
		Opening:          900,
		PhysicalCapacity: 1_000,
		SafePercentage:   95, // safe = 950
		From:             day(2027, time.January, 1),
		Days: []PlanDay{
			{Date: day(2027, time.January, 1), In: 20}, // 920 - under safe
			{Date: day(2027, time.January, 2), In: 20}, // 940 - under safe
			{Date: day(2027, time.January, 3), In: 20}, // 960 - over safe
			{Date: day(2027, time.January, 4), In: 20}, // 980
			{Date: day(2027, time.January, 5), In: 40}, // 1020 - over physical
		},
		Bands: DefaultBands,
	}
	p := Project(in)
	if p.FirstSafeBreach == nil || !p.FirstSafeBreach.Equal(day(2027, time.January, 3)) {
		t.Errorf("first safe breach = %v, want 2027-01-03", p.FirstSafeBreach)
	}
	if p.FirstPhysicalBreach == nil || !p.FirstPhysicalBreach.Equal(day(2027, time.January, 5)) {
		t.Errorf("first physical breach = %v, want 2027-01-05", p.FirstPhysicalBreach)
	}
	almost(t, p.PeakClosing, 1_020, "peak")
}

func TestProjectHasNoBreachWhenTheCurveStaysFlat(t *testing.T) {
	in := ProjectInput{
		Opening:          500,
		PhysicalCapacity: 1_000,
		SafePercentage:   95,
		From:             day(2027, time.January, 1),
		Days: []PlanDay{
			{Date: day(2027, time.January, 1), In: 100, Out: 100},
			{Date: day(2027, time.January, 2), In: 100, Out: 100},
		},
		Bands: DefaultBands,
	}
	p := Project(in)
	if p.FirstSafeBreach != nil || p.FirstPhysicalBreach != nil {
		t.Errorf("expected no breach, got safe=%v physical=%v", p.FirstSafeBreach, p.FirstPhysicalBreach)
	}
}

func TestProjectHorizons(t *testing.T) {
	days := make([]PlanDay, 0, 40)
	for i := 0; i < 40; i++ {
		days = append(days, PlanDay{Date: day(2027, time.January, 1).AddDate(0, 0, i), In: 10})
	}
	p := Project(ProjectInput{
		Opening:          0,
		PhysicalCapacity: 1_000,
		SafePercentage:   100,
		From:             day(2027, time.January, 1),
		Days:             days,
		Bands:            DefaultBands,
	})

	want := map[string]float64{"TODAY": 10, "D1": 20, "D3": 40, "D7": 80, "EOM": 310, "EOS": 400}
	for _, h := range p.Horizons {
		if w, ok := want[h.Key]; ok {
			almost(t, h.Closing, w, "horizon "+h.Key)
			delete(want, h.Key)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing horizons: %v", want)
	}
}

// TestProjectAgainstTheFinishedGoodsPlan reproduces the finding in the season
// summary: with 500 t/day of quota sales, the 69,000 t finished sugar pool is
// first exceeded on 28 May 2027.
func TestProjectAgainstTheFinishedGoodsPlan(t *testing.T) {
	// From 19 Feb 2027 the plan runs at a steady 900 t/day in (refined 400 +
	// white 500) and 500 t/day of quota sales out, opening at 29,700 t.
	start := day(2027, time.February, 19)
	days := make([]PlanDay, 0, 200)
	for i := 0; i < 200; i++ {
		days = append(days, PlanDay{Date: start.AddDate(0, 0, i), In: 900, Out: 500})
	}
	p := Project(ProjectInput{
		ScopeCode:        "FG-POOL",
		Opening:          29_700,
		PhysicalCapacity: 69_000,
		SafePercentage:   100, // compare against the physical pool ceiling
		From:             start,
		Days:             days,
		Bands:            DefaultBands,
	})
	if p.FirstPhysicalBreach == nil {
		t.Fatal("expected the finished goods pool to overflow")
	}
	if got := *p.FirstPhysicalBreach; !got.Equal(day(2027, time.May, 28)) {
		t.Errorf("first overflow = %v, want 2027-05-28", got.Format("2006-01-02"))
	}
}
