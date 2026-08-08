package service

import (
	"errors"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) *time.Time {
	t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func num(v float64) *float64 { return &v }

// --- storage location ------------------------------------------------------

func TestStorageLocationInputAcceptsAValidWarehouse(t *testing.T) {
	in := StorageLocationInput{
		FactoryID:              1,
		StorageCode:            "  FG-WH04  ",
		StorageName:            "Finished Sugar Warehouse 4",
		StorageTypeCode:        "FINISHED_GOODS_WAREHOUSE",
		PhysicalCapacity:       30_000,
		SafeCapacityPercentage: 95,
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if in.StorageCode != "FG-WH04" {
		t.Errorf("storage code = %q, want it trimmed to FG-WH04", in.StorageCode)
	}
	if in.Status != "ACTIVE" {
		t.Errorf("status defaulted to %q, want ACTIVE", in.Status)
	}
	if in.CapacityUOM != "TON" {
		t.Errorf("capacity uom defaulted to %q, want TON", in.CapacityUOM)
	}
}

func TestStorageLocationInputRejections(t *testing.T) {
	base := func() StorageLocationInput {
		return StorageLocationInput{
			FactoryID: 1, StorageCode: "X", StorageName: "X",
			StorageTypeCode: "TANK", PhysicalCapacity: 100, SafeCapacityPercentage: 95,
		}
	}

	cases := []struct {
		name   string
		mutate func(*StorageLocationInput)
	}{
		{"no factory", func(in *StorageLocationInput) { in.FactoryID = 0 }},
		{"no code", func(in *StorageLocationInput) { in.StorageCode = "  " }},
		{"no name", func(in *StorageLocationInput) { in.StorageName = "" }},
		{"no type", func(in *StorageLocationInput) { in.StorageTypeCode = "" }},
		{"negative capacity", func(in *StorageLocationInput) { in.PhysicalCapacity = -1 }},
		{"safe percentage of zero", func(in *StorageLocationInput) { in.SafeCapacityPercentage = 0 }},
		{"safe percentage above 100", func(in *StorageLocationInput) { in.SafeCapacityPercentage = 101 }},
		{"negative minimum", func(in *StorageLocationInput) { in.MinimumStockLevel = -1 }},
		{"minimum above capacity", func(in *StorageLocationInput) { in.MinimumStockLevel = 200 }},
		{"unknown status", func(in *StorageLocationInput) { in.Status = "ARCHIVED" }},
		{"effective range inverted", func(in *StorageLocationInput) {
			in.EffectiveFrom = day(2027, time.March, 1)
			in.EffectiveTo = day(2027, time.February, 1)
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mutate(&in)
			err := in.Validate()
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("error should wrap ErrValidation, got %v", err)
			}
		})
	}
}

// --- storage product capacity ----------------------------------------------

func TestStorageProductCapacityNeedsAtLeastOneCeiling(t *testing.T) {
	in := StorageProductCapacityInput{
		StorageCode: "FG-WH01", ProductCode: "REFINED-SUGAR", PackagingCode: "PKG-50KG",
	}
	err := in.Validate()
	if err == nil {
		t.Fatal("a capacity row with no ceiling is meaningless and must be rejected")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("error should wrap ErrValidation, got %v", err)
	}
}

func TestStorageProductCapacityAcceptsEitherAxis(t *testing.T) {
	packagesOnly := StorageProductCapacityInput{
		StorageCode: "FG-WH01", ProductCode: "REFINED-SUGAR", PackagingCode: "PKG-50KG",
		MaximumPackageQty: num(200_000),
	}
	if err := packagesOnly.Validate(); err != nil {
		t.Errorf("a package-only ceiling should be valid, got %v", err)
	}

	weightOnly := StorageProductCapacityInput{
		StorageCode: "CON-S01", ProductCode: "REFINED-SUGAR", PackagingCode: "PKG-BULK",
		MaximumWeightQty: num(2_000),
	}
	if err := weightOnly.Validate(); err != nil {
		t.Errorf("a weight-only ceiling should be valid, got %v", err)
	}
}

func TestStorageProductCapacityRejectsSafeAboveMaximum(t *testing.T) {
	in := StorageProductCapacityInput{
		StorageCode: "FG-WH01", ProductCode: "REFINED-SUGAR", PackagingCode: "PKG-50KG",
		MaximumWeightQty: num(10_000), MaximumSafeQty: num(12_000),
	}
	if err := in.Validate(); err == nil {
		t.Fatal("a safe ceiling above the maximum is contradictory")
	}
}

// --- packaging master ------------------------------------------------------

func TestPackagingTypeDerivesWeightInTon(t *testing.T) {
	cases := []struct {
		name   string
		weight float64
		uom    string
		want   float64
	}{
		{"50 KG bag", 50, "KG", 0.050},
		{"jumbo bag", 1100, "KG", 1.100},
		{"25 KG bag", 25, "KG", 0.025},
		{"already in tons", 1.1, "TON", 1.1},
		{"unit defaults to KG", 500, "", 0.500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := PackagingTypeInput{Code: "PKG-X", Description: c.name, NetWeight: c.weight, NetWeightUOM: c.uom}
			if err := in.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if diff := in.WeightInTon() - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("weightInTon = %v, want %v", in.WeightInTon(), c.want)
			}
		})
	}
}

func TestPackagingTypeRejectsAWeightlessBag(t *testing.T) {
	// A non-bulk package weighing nothing would make bags/tons undefined.
	in := PackagingTypeInput{Code: "PKG-X", Description: "Broken", NetWeight: 0}
	if err := in.Validate(); err == nil {
		t.Fatal("a non-bulk package must weigh something")
	}
}

func TestPackagingTypeRejectsBulkWithWeight(t *testing.T) {
	in := PackagingTypeInput{Code: "PKG-BULK", Description: "Bulk", NetWeight: 50, IsBulk: true}
	if err := in.Validate(); err == nil {
		t.Fatal("bulk packaging must have no per-package weight")
	}
}

func TestPackagingTypeRejectsAnUnknownUnit(t *testing.T) {
	in := PackagingTypeInput{Code: "PKG-X", Description: "X", NetWeight: 50, NetWeightUOM: "LB"}
	if err := in.Validate(); err == nil {
		t.Fatal("only KG and TON are convertible here")
	}
}

// --- alert thresholds ------------------------------------------------------

func validBands() ThresholdBandsInput {
	return ThresholdBandsInput{Bands: []ThresholdBandInput{
		{Code: "NORMAL", Severity: "NORMAL", From: 0, To: num(80)},
		{Code: "WARNING", Severity: "WARNING", From: 80, To: num(90)},
		{Code: "HIGH", Severity: "HIGH", From: 90, To: num(95)},
		{Code: "CRITICAL", Severity: "CRITICAL", From: 95, To: num(100)},
		{Code: "FULL", Severity: "FULL", From: 100},
	}}
}

func TestThresholdBandsAcceptAContiguousCover(t *testing.T) {
	in := validBands()
	if err := in.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if in.Bands[0].Name != "NORMAL" {
		t.Errorf("name should default to the code, got %q", in.Bands[0].Name)
	}
}

func TestThresholdBandsSortThemselves(t *testing.T) {
	in := validBands()
	in.Bands[0], in.Bands[4] = in.Bands[4], in.Bands[0]
	if err := in.Validate(); err != nil {
		t.Fatalf("bands supplied out of order should still validate, got %v", err)
	}
	if in.Bands[0].Code != "NORMAL" || in.Bands[4].Code != "FULL" {
		t.Errorf("bands were not sorted: %v .. %v", in.Bands[0].Code, in.Bands[4].Code)
	}
}

func TestThresholdBandsRejectAGap(t *testing.T) {
	// A utilisation of 85% would fall into no band at all.
	in := validBands()
	in.Bands[1].From = 82
	if err := in.Validate(); err == nil {
		t.Fatal("a gap between bands must be rejected")
	}
}

func TestThresholdBandsRejectAnOverlap(t *testing.T) {
	in := validBands()
	in.Bands[1].From = 75
	if err := in.Validate(); err == nil {
		t.Fatal("overlapping bands make classification ambiguous")
	}
}

func TestThresholdBandsMustStartAtZero(t *testing.T) {
	in := validBands()
	in.Bands[0].From = 10
	if err := in.Validate(); err == nil {
		t.Fatal("an empty warehouse must still land in a band")
	}
}

func TestThresholdBandsMustBeOpenEnded(t *testing.T) {
	// Without an unbounded top band, an over-capacity warehouse has no band.
	in := validBands()
	in.Bands[4].To = num(200)
	if err := in.Validate(); err == nil {
		t.Fatal("the highest band must have no upper bound")
	}
}

func TestThresholdBandsRejectAnUnknownSeverity(t *testing.T) {
	in := validBands()
	in.Bands[2].Severity = "SEVERE"
	if err := in.Validate(); err == nil {
		t.Fatal("severity must be one of the known values")
	}
}

func TestThresholdBandsRejectDuplicateCodes(t *testing.T) {
	in := validBands()
	in.Bands[2].Code = "WARNING"
	if err := in.Validate(); err == nil {
		t.Fatal("duplicate band codes must be rejected")
	}
}

func TestThresholdBandsRejectAnEmptySet(t *testing.T) {
	in := ThresholdBandsInput{}
	if err := in.Validate(); err == nil {
		t.Fatal("clearing every band would leave nothing to classify against")
	}
}

// --- daily storage plan ----------------------------------------------------

func TestStoragePlanLineDefaults(t *testing.T) {
	in := StoragePlanLineInput{
		FactoryID: 1, ScopeCode: "FG-POOL",
		PlanDate: time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if in.Status != "APPROVED" {
		t.Errorf("status defaulted to %q, want APPROVED", in.Status)
	}
}

func TestStoragePlanLineRejections(t *testing.T) {
	base := func() StoragePlanLineInput {
		return StoragePlanLineInput{
			FactoryID: 1, ScopeCode: "FG-POOL",
			PlanDate: time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC),
		}
	}
	cases := []struct {
		name   string
		mutate func(*StoragePlanLineInput)
	}{
		{"no factory", func(in *StoragePlanLineInput) { in.FactoryID = 0 }},
		{"no scope", func(in *StoragePlanLineInput) { in.ScopeCode = "" }},
		{"no date", func(in *StoragePlanLineInput) { in.PlanDate = time.Time{} }},
		{"negative in", func(in *StoragePlanLineInput) { in.PlannedInWeight = -1 }},
		{"negative out", func(in *StoragePlanLineInput) { in.PlannedOutWeight = -1 }},
		{"packaging without product", func(in *StoragePlanLineInput) { in.PackagingCode = "PKG-50KG" }},
		{"unknown status", func(in *StoragePlanLineInput) { in.Status = "SUBMITTED" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mutate(&in)
			if err := in.Validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}
