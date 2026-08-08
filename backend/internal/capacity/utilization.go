package capacity

import "sort"

// ThresholdBand is one configurable alert band (requirement section 18).
//
// Bands are half-open: [From, To). The topmost band leaves To nil, meaning
// "and above".
type ThresholdBand struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Severity string   `json:"severity"`
	UIState  string   `json:"uiState"`
	From     float64  `json:"fromPercentage"`
	To       *float64 `json:"toPercentage,omitempty"`
}

// Severity values, ordered from calm to alarming.
const (
	SeverityNormal   = "NORMAL"
	SeverityWarning  = "WARNING"
	SeverityHigh     = "HIGH"
	SeverityCritical = "CRITICAL"
	SeverityFull     = "FULL"
)

// DefaultBands is the fallback used when a factory has no configured bands.
// The database seed carries the same values; this exists so the engine never
// silently reports everything as normal because a config row is missing.
var DefaultBands = []ThresholdBand{
	{Code: "NORMAL", Name: "Normal", Severity: SeverityNormal, UIState: "Success", From: 0, To: ptr(80)},
	{Code: "WARNING", Name: "Warning", Severity: SeverityWarning, UIState: "Warning", From: 80, To: ptr(90)},
	{Code: "HIGH", Name: "High", Severity: SeverityHigh, UIState: "Warning", From: 90, To: ptr(95)},
	{Code: "CRITICAL", Name: "Critical", Severity: SeverityCritical, UIState: "Error", From: 95, To: ptr(100)},
	{Code: "FULL", Name: "Full", Severity: SeverityFull, UIState: "Error", From: 100},
}

func ptr(v float64) *float64 { return &v }

// Classify returns the band a utilisation percentage falls into.
//
// Bands are sorted by their lower bound, so a caller may pass them in any
// order. A percentage below the lowest band takes that lowest band; anything
// above the highest takes the highest. If no bands are supplied, DefaultBands
// is used rather than returning nothing.
func Classify(bands []ThresholdBand, percentage float64) ThresholdBand {
	if len(bands) == 0 {
		bands = DefaultBands
	}
	sorted := make([]ThresholdBand, len(bands))
	copy(sorted, bands)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].From < sorted[j].From })

	best := sorted[0]
	for _, b := range sorted {
		if percentage+Epsilon >= b.From {
			best = b
		}
	}
	return best
}

// Utilization is the capacity picture for one storage scope
// (requirement section 17).
type Utilization struct {
	// Capacity is the physical ceiling.
	Capacity float64 `json:"capacity"`
	// SafeCapacity is Capacity x safe percentage — the ceiling for normal
	// operation.
	SafeCapacity float64 `json:"safeCapacity"`
	// CurrentStock is what is in there now.
	CurrentStock float64 `json:"currentStock"`
	// ReservedStock is committed but not yet issued.
	ReservedStock float64 `json:"reservedStock"`
	// AvailableStock is CurrentStock less ReservedStock — what can still be
	// sold or issued.
	AvailableStock float64 `json:"availableStock"`
	// AvailableCapacity is the room left before the safe ceiling.
	//
	//	Available Capacity = Maximum Allowed Capacity - Current Stock
	AvailableCapacity float64 `json:"availableCapacity"`
	// AvailablePhysical is the room left before the physical ceiling.
	AvailablePhysical float64 `json:"availablePhysicalCapacity"`
	// UtilizationPct is Current Stock / Physical Capacity x 100.
	UtilizationPct float64       `json:"utilizationPercentage"`
	Band           ThresholdBand `json:"band"`
	UOM            string        `json:"uom"`
}

// Utilize computes the capacity picture for a scope.
//
// safePercentage is the share of physical capacity usable under normal
// operation; a value outside (0, 100] is treated as 100.
func Utilize(currentStock, reservedStock, physicalCapacity, safePercentage float64, bands []ThresholdBand, uom string) Utilization {
	if safePercentage <= 0 || safePercentage > 100 {
		safePercentage = 100
	}
	safeCapacity := physicalCapacity * safePercentage / 100
	pct := Percentage(currentStock, physicalCapacity)

	return Utilization{
		Capacity:          Round(physicalCapacity, 3),
		SafeCapacity:      Round(safeCapacity, 3),
		CurrentStock:      Round(currentStock, 3),
		ReservedStock:     Round(reservedStock, 3),
		AvailableStock:    Round(currentStock-reservedStock, 3),
		AvailableCapacity: Round(safeCapacity-currentStock, 3),
		AvailablePhysical: Round(physicalCapacity-currentStock, 3),
		UtilizationPct:    Round(pct, 2),
		Band:              Classify(bands, pct),
		UOM:               uom,
	}
}
