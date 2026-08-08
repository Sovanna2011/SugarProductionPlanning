package capacity

import (
	"fmt"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// Check codes reported by Validate (requirement section 24).
const (
	CheckPhysicalCapacity = "PHYSICAL_CAPACITY"
	CheckProductCapacity  = "PRODUCT_CAPACITY"
	CheckPackageCapacity  = "PACKAGE_CAPACITY"
	CheckSafeCapacity     = "SAFE_CAPACITY"
	CheckProductAllowed   = "PRODUCT_ALLOWED"
	CheckMixedProducts    = "MIXED_PRODUCTS"
	CheckMinimumStock     = "MINIMUM_STOCK"
)

// Finding severities.
const (
	FindingOK      = "OK"
	FindingWarning = "WARNING"
	FindingBlock   = "BLOCK"
)

// LocationState is what the location holds right now, across all products.
type LocationState struct {
	Location domain.StorageLocation
	// TotalWeight currently stored in the location, all products and batches.
	TotalWeight float64
	// ProductIDsOnHand are the distinct products currently stored. Used for the
	// mixed-products rule.
	ProductIDsOnHand []int64
}

// ProductState is what the location holds of one product + packaging, plus the
// configured ceiling for that combination.
type ProductState struct {
	// Capacity is the storage_product_capacity row in force. Nil means no row
	// is configured, which is read as "this product is not permitted here".
	Capacity *domain.StorageProductCapacity
	// Current is the stock on hand for this product + packaging in this
	// location, summed across batches.
	Current Quantity
}

// Receipt is the incoming quantity to validate.
type Receipt struct {
	ProductID       int64
	ProductName     string
	PackagingTypeID int64
	PackagingCode   string
	Quantity        Quantity
}

// Finding is one rule outcome.
type Finding struct {
	Check     string  `json:"check"`
	Severity  string  `json:"severity"`
	Message   string  `json:"message"`
	Limit     float64 `json:"limit"`
	Projected float64 `json:"projected"`
	Excess    float64 `json:"excess"`
	UOM       string  `json:"uom"`
}

// ValidationResult is the answer to "may this receipt be posted?".
type ValidationResult struct {
	// Allowed is true when nothing blocks the posting.
	Allowed bool `json:"allowed"`
	// RequiresOverride is true when the posting is blocked but an authorised
	// business rule may permit it anyway (requirement section 24.5).
	RequiresOverride bool `json:"requiresOverride"`
	// Findings lists every rule that fired, blocking or not.
	Findings []Finding `json:"findings"`
}

// Blocking returns just the blocking findings.
func (r ValidationResult) Blocking() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == FindingBlock {
			out = append(out, f)
		}
	}
	return out
}

// Validate applies the pre-posting checks of requirement section 24 to a
// receipt: physical capacity, product capacity, packaging capacity and safe
// capacity, plus the mixed-products rule from the location master.
//
// It never mutates its inputs and never talks to a store; the caller loads the
// state, calls Validate, and decides what to do with the result.
func Validate(loc LocationState, prod ProductState, receipt Receipt, bands []ThresholdBand) ValidationResult {
	uom := loc.Location.CapacityUOM
	if uom == "" {
		uom = "TON"
	}
	res := ValidationResult{Allowed: true}

	add := func(f Finding) {
		if f.Severity == FindingBlock {
			res.Allowed = false
			res.RequiresOverride = true
		}
		res.Findings = append(res.Findings, f)
	}

	// A withdrawal or a zero-quantity line has nothing to validate against a
	// ceiling.
	if receipt.Quantity.Weight <= Epsilon && receipt.Quantity.Packages <= Epsilon {
		return res
	}

	// The location itself must be usable.
	if loc.Location.Status != domain.StatusActive {
		add(Finding{
			Check:    CheckProductAllowed,
			Severity: FindingBlock,
			Message: fmt.Sprintf("Storage location %s (%s) is not active",
				loc.Location.StorageCode, loc.Location.StorageName),
			UOM: uom,
		})
		return res
	}

	// --- 5. Physical maximum -------------------------------------------------
	// Checked against everything already in the location, not just this
	// product: product ceilings are limits, not additive capacity
	// (requirement section 13).
	projectedTotal := loc.TotalWeight + receipt.Quantity.Weight
	if projectedTotal > loc.Location.PhysicalCapacity+Epsilon {
		add(Finding{
			Check:    CheckPhysicalCapacity,
			Severity: FindingBlock,
			Message: fmt.Sprintf("%s would hold %s %s against a physical capacity of %s %s",
				loc.Location.StorageName, num(projectedTotal), uom,
				num(loc.Location.PhysicalCapacity), uom),
			Limit:     Round(loc.Location.PhysicalCapacity, 3),
			Projected: Round(projectedTotal, 3),
			Excess:    Round(projectedTotal-loc.Location.PhysicalCapacity, 3),
			UOM:       uom,
		})
	}

	// --- 4. Safe capacity ----------------------------------------------------
	safePct := loc.Location.SafeCapacityPercentage
	if safePct <= 0 || safePct > 100 {
		safePct = 100
	}
	safeCapacity := loc.Location.PhysicalCapacity * safePct / 100
	if projectedTotal > safeCapacity+Epsilon && projectedTotal <= loc.Location.PhysicalCapacity+Epsilon {
		add(Finding{
			Check:    CheckSafeCapacity,
			Severity: FindingWarning,
			Message: fmt.Sprintf("%s would reach %s%% of capacity, above the safe level of %s%%",
				loc.Location.StorageName, num(Percentage(projectedTotal, loc.Location.PhysicalCapacity)), num(safePct)),
			Limit:     Round(safeCapacity, 3),
			Projected: Round(projectedTotal, 3),
			Excess:    Round(projectedTotal-safeCapacity, 3),
			UOM:       uom,
		})
	}

	// --- Mixed products ------------------------------------------------------
	if !loc.Location.AllowMixedProducts {
		for _, id := range loc.ProductIDsOnHand {
			if id != receipt.ProductID {
				add(Finding{
					Check:    CheckMixedProducts,
					Severity: FindingBlock,
					Message: fmt.Sprintf("%s does not allow mixed products and already holds another product",
						loc.Location.StorageName),
					UOM: uom,
				})
				break
			}
		}
	}

	// --- 2 & 3. Product and packaging capacity -------------------------------
	if prod.Capacity == nil {
		// No configured row means master data does not permit this product and
		// packaging in this location.
		add(Finding{
			Check:    CheckProductAllowed,
			Severity: FindingBlock,
			Message: fmt.Sprintf("No capacity is configured for %s in %s packaging at %s",
				receipt.ProductName, receipt.PackagingCode, loc.Location.StorageName),
			UOM: uom,
		})
		return res
	}
	if prod.Capacity.Status != domain.StatusActive {
		add(Finding{
			Check:    CheckProductAllowed,
			Severity: FindingBlock,
			Message: fmt.Sprintf("The capacity configuration for %s in %s packaging at %s is inactive",
				receipt.ProductName, receipt.PackagingCode, loc.Location.StorageName),
			UOM: uom,
		})
		return res
	}

	projectedProduct := prod.Current.Add(receipt.Quantity)

	if max := prod.Capacity.MaximumWeightQty; max != nil && projectedProduct.Weight > *max+Epsilon {
		add(Finding{
			Check:    CheckProductCapacity,
			Severity: FindingBlock,
			Message: fmt.Sprintf("%s in %s would hold %s %s of %s against a product limit of %s %s",
				loc.Location.StorageName, receipt.PackagingCode, num(projectedProduct.Weight), uom,
				receipt.ProductName, num(*max), uom),
			Limit:     Round(*max, 3),
			Projected: Round(projectedProduct.Weight, 3),
			Excess:    Round(projectedProduct.Weight-*max, 3),
			UOM:       uom,
		})
	}

	// Package ceilings only make sense where packages are counted.
	if max := prod.Capacity.MaximumPackageQty; max != nil && loc.Location.TracksPackages &&
		projectedProduct.Packages > *max+Epsilon {
		add(Finding{
			Check:    CheckPackageCapacity,
			Severity: FindingBlock,
			Message: fmt.Sprintf("%s would hold %s %s packages of %s against a limit of %s",
				loc.Location.StorageName, num(projectedProduct.Packages), receipt.PackagingCode,
				receipt.ProductName, num(*max)),
			Limit:     Round(*max, 3),
			Projected: Round(projectedProduct.Packages, 3),
			Excess:    Round(projectedProduct.Packages-*max, 3),
			UOM:       "PACKAGE",
		})
	}

	// A product-level safe ceiling, when configured, warns the same way the
	// location-level one does.
	if safe := prod.Capacity.MaximumSafeQty; safe != nil && projectedProduct.Weight > *safe+Epsilon {
		add(Finding{
			Check:    CheckSafeCapacity,
			Severity: FindingWarning,
			Message: fmt.Sprintf("%s of %s would exceed its safe level of %s %s",
				receipt.ProductName, loc.Location.StorageName, num(*safe), uom),
			Limit:     Round(*safe, 3),
			Projected: Round(projectedProduct.Weight, 3),
			Excess:    Round(projectedProduct.Weight-*safe, 3),
			UOM:       uom,
		})
	}

	return res
}

// num formats a quantity for an operator-facing message, dropping trailing
// zeros so "10000.000" reads as "10000".
func num(v float64) string {
	return trimZeros(fmt.Sprintf("%.3f", v))
}

func trimZeros(s string) string {
	i := len(s)
	for i > 0 && s[i-1] == '0' {
		i--
	}
	if i > 0 && s[i-1] == '.' {
		i--
	}
	return s[:i]
}
