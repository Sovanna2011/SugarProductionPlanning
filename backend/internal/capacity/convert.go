// Package capacity implements the storage capacity rules: package/weight
// conversion, utilisation and alert banding, receipt validation and forward
// projection.
//
// Everything in this package is pure: it takes master data and quantities and
// returns results. No database, no HTTP. That is what makes the rules testable
// and what keeps configurable values (package sizes, thresholds, capacities)
// out of the code.
package capacity

import "math"

// Epsilon is the tolerance used when comparing quantities. Business quantities
// are tons to three decimals, so anything below a gram is noise from float
// arithmetic rather than a real difference.
const Epsilon = 1e-6

// Quantity carries both axes the system must always maintain
// (requirement section 14).
type Quantity struct {
	// Packages is a discrete bag count. Zero for bulk storage.
	Packages float64 `json:"packages"`
	// Weight is in tons.
	Weight float64 `json:"weight"`
}

// Add returns the sum of two quantities.
func (q Quantity) Add(other Quantity) Quantity {
	return Quantity{Packages: q.Packages + other.Packages, Weight: q.Weight + other.Weight}
}

// Sub returns q less other.
func (q Quantity) Sub(other Quantity) Quantity {
	return Quantity{Packages: q.Packages - other.Packages, Weight: q.Weight - other.Weight}
}

// IsZero reports whether both axes are zero within Epsilon.
func (q Quantity) IsZero() bool {
	return math.Abs(q.Packages) < Epsilon && math.Abs(q.Weight) < Epsilon
}

// WeightOf converts a package count to tons.
//
//	Total Weight = Package Quantity x Net Weight per Package
//
// weightPerPackage comes from the packaging master, never from a constant.
func WeightOf(packages, weightPerPackage float64) float64 {
	return packages * weightPerPackage
}

// PackagesOf converts tons to a package count. It returns 0 for bulk packaging
// (weightPerPackage of zero), which has no meaningful package count.
func PackagesOf(weight, weightPerPackage float64) float64 {
	if weightPerPackage <= 0 {
		return 0
	}
	return weight / weightPerPackage
}

// FromPackages builds a Quantity from a package count, deriving the weight.
func FromPackages(packages, weightPerPackage float64) Quantity {
	return Quantity{Packages: packages, Weight: WeightOf(packages, weightPerPackage)}
}

// FromWeight builds a Quantity from a weight, deriving the package count.
// For bulk packaging the package axis stays at zero.
func FromWeight(weight, weightPerPackage float64) Quantity {
	return Quantity{Packages: PackagesOf(weight, weightPerPackage), Weight: weight}
}

// Percentage returns value/total as a percentage, guarding against a zero
// divisor. A non-zero value against zero capacity is reported as 100% full,
// since there is no room for it at all.
func Percentage(value, total float64) float64 {
	if total <= Epsilon {
		if value > Epsilon {
			return 100
		}
		return 0
	}
	return value / total * 100
}

// Round returns v rounded to the given number of decimal places. Used when
// handing figures to the UI so that 79.99999999 does not surface as such.
func Round(v float64, places int) float64 {
	factor := math.Pow(10, float64(places))
	return math.Round(v*factor) / factor
}
