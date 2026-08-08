package capacity

import "time"

// PlanDay is one day of planned movement for a storage scope.
type PlanDay struct {
	Date time.Time `json:"date"`
	In   float64   `json:"in"`
	Out  float64   `json:"out"`
}

// ProjectedDay is the expected position at the end of one planned day.
type ProjectedDay struct {
	Date            time.Time     `json:"date"`
	Opening         float64       `json:"opening"`
	PlannedIn       float64       `json:"plannedIn"`
	PlannedOut      float64       `json:"plannedOut"`
	Closing         float64       `json:"closing"`
	UtilizationPct  float64       `json:"utilizationPercentage"`
	Band            ThresholdBand `json:"band"`
	ExceedsSafe     bool          `json:"exceedsSafe"`
	ExceedsPhysical bool          `json:"exceedsPhysical"`
}

// Horizon is a named point in the projection that management asks for
// (requirement section 19).
type Horizon struct {
	Key            string        `json:"key"`
	Label          string        `json:"label"`
	Date           *time.Time    `json:"date,omitempty"`
	Closing        float64       `json:"closing"`
	UtilizationPct float64       `json:"utilizationPercentage"`
	Available      float64       `json:"availableCapacity"`
	Band           ThresholdBand `json:"band"`
}

// Projection is the forward look for one storage scope.
type Projection struct {
	ScopeCode    string         `json:"scopeCode"`
	ScopeName    string         `json:"scopeName"`
	Capacity     float64        `json:"capacity"`
	SafeCapacity float64        `json:"safeCapacity"`
	Opening      float64        `json:"opening"`
	UOM          string         `json:"uom"`
	Days         []ProjectedDay `json:"days"`
	Horizons     []Horizon      `json:"horizons"`
	// FirstSafeBreach is the first date the scope is expected to exceed its
	// safe capacity — the date the requirement asks to have highlighted.
	FirstSafeBreach *time.Time `json:"firstSafeBreachDate,omitempty"`
	// FirstPhysicalBreach is the first date it is expected to physically
	// overflow.
	FirstPhysicalBreach *time.Time `json:"firstPhysicalBreachDate,omitempty"`
	PeakClosing         float64    `json:"peakClosing"`
	PeakDate            *time.Time `json:"peakDate,omitempty"`
}

// ProjectInput describes what to project.
type ProjectInput struct {
	ScopeCode string
	ScopeName string
	// Opening is the stock the projection starts from — normally the actual
	// balance today, not the plan's own opening figure.
	Opening          float64
	PhysicalCapacity float64
	SafePercentage   float64
	UOM              string
	// Days must be supplied in ascending date order.
	Days  []PlanDay
	Bands []ThresholdBand
	// From is the date the projection starts, used to label the relative
	// horizons. Zero means "the first planned day".
	From time.Time
}

// Project rolls an opening balance forward through the approved daily plan.
//
//	Planned Closing = Planned Opening + Planned Stock In - Planned Stock Out
//
// Each day's opening is the previous day's closing, so a single actual balance
// today drives the whole forward curve.
func Project(in ProjectInput) Projection {
	safePct := in.SafePercentage
	if safePct <= 0 || safePct > 100 {
		safePct = 100
	}
	safeCapacity := in.PhysicalCapacity * safePct / 100
	uom := in.UOM
	if uom == "" {
		uom = "TON"
	}

	p := Projection{
		ScopeCode:    in.ScopeCode,
		ScopeName:    in.ScopeName,
		Capacity:     Round(in.PhysicalCapacity, 3),
		SafeCapacity: Round(safeCapacity, 3),
		Opening:      Round(in.Opening, 3),
		UOM:          uom,
		Days:         make([]ProjectedDay, 0, len(in.Days)),
	}

	balance := in.Opening
	for _, day := range in.Days {
		opening := balance
		closing := opening + day.In - day.Out
		balance = closing

		pct := Percentage(closing, in.PhysicalCapacity)
		pd := ProjectedDay{
			Date:            day.Date,
			Opening:         Round(opening, 3),
			PlannedIn:       Round(day.In, 3),
			PlannedOut:      Round(day.Out, 3),
			Closing:         Round(closing, 3),
			UtilizationPct:  Round(pct, 2),
			Band:            Classify(in.Bands, pct),
			ExceedsSafe:     closing > safeCapacity+Epsilon,
			ExceedsPhysical: closing > in.PhysicalCapacity+Epsilon,
		}
		p.Days = append(p.Days, pd)

		if pd.ExceedsSafe && p.FirstSafeBreach == nil {
			d := day.Date
			p.FirstSafeBreach = &d
		}
		if pd.ExceedsPhysical && p.FirstPhysicalBreach == nil {
			d := day.Date
			p.FirstPhysicalBreach = &d
		}
		if p.PeakDate == nil || closing > p.PeakClosing {
			d := day.Date
			p.PeakClosing = Round(closing, 3)
			p.PeakDate = &d
		}
	}

	p.Horizons = buildHorizons(p, in, safeCapacity)
	return p
}

// buildHorizons picks out the named snapshots management asks for: today,
// +1/+3/+7 days, end of month and end of season.
func buildHorizons(p Projection, in ProjectInput, safeCapacity float64) []Horizon {
	from := in.From
	if from.IsZero() && len(in.Days) > 0 {
		from = in.Days[0].Date
	}
	if len(p.Days) == 0 {
		return nil
	}

	// The day-level lookup: latest projected day on or before a target date.
	at := func(target time.Time) *ProjectedDay {
		var found *ProjectedDay
		for i := range p.Days {
			if !p.Days[i].Date.After(target) {
				found = &p.Days[i]
				continue
			}
			break
		}
		return found
	}

	endOfMonth := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location()).
		AddDate(0, 1, -1)
	endOfSeason := p.Days[len(p.Days)-1].Date

	specs := []struct {
		key, label string
		date       time.Time
	}{
		{"TODAY", "Today", from},
		{"D1", "+1 Day", from.AddDate(0, 0, 1)},
		{"D3", "+3 Days", from.AddDate(0, 0, 3)},
		{"D7", "+7 Days", from.AddDate(0, 0, 7)},
		{"EOM", "End of Month", endOfMonth},
		{"EOS", "End of Season", endOfSeason},
	}

	horizons := make([]Horizon, 0, len(specs))
	for _, s := range specs {
		h := Horizon{Key: s.key, Label: s.label}
		if d := at(s.date); d != nil {
			date := d.Date
			h.Date = &date
			h.Closing = d.Closing
			h.UtilizationPct = d.UtilizationPct
			h.Available = Round(safeCapacity-d.Closing, 3)
			h.Band = d.Band
		} else {
			// The horizon falls before the plan starts: nothing has moved yet.
			h.Closing = p.Opening
			h.UtilizationPct = Round(Percentage(p.Opening, in.PhysicalCapacity), 2)
			h.Available = Round(safeCapacity-p.Opening, 3)
			h.Band = Classify(in.Bands, h.UtilizationPct)
		}
		horizons = append(horizons, h)
	}
	return horizons
}
