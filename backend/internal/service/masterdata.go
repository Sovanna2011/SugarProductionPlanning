package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/capacity"
	"github.com/sovanna2011/sugarproductionplanning/backend/internal/domain"
)

// This file holds the write side of master data. The requirement states six
// times that capacities, packages and thresholds must be configurable; that is
// only true if they can be maintained through the application rather than by
// editing SQL.
//
// Every input validates itself before touching the database. The Validate
// methods are pure, so the rules are unit tested without a database.

// Warning is a non-blocking observation about a configuration change: it is
// saved, but somebody should look at it.
type Warning struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// StorageLocationInput creates or updates a tank, silo or warehouse.
type StorageLocationInput struct {
	FactoryID              int64      `json:"factoryId"`
	StorageCode            string     `json:"storageCode"`
	StorageName            string     `json:"storageName"`
	StorageTypeCode        string     `json:"storageTypeCode"`
	PhysicalCapacity       float64    `json:"physicalCapacity"`
	CapacityUOM            string     `json:"capacityUom"`
	MinimumStockLevel      float64    `json:"minimumStockLevel"`
	SafeCapacityPercentage float64    `json:"safeCapacityPercentage"`
	AllowMixedProducts     bool       `json:"allowMixedProducts"`
	AllowMixedBatches      bool       `json:"allowMixedBatches"`
	Status                 string     `json:"status"`
	EffectiveFrom          *time.Time `json:"effectiveFrom,omitempty"`
	EffectiveTo            *time.Time `json:"effectiveTo,omitempty"`
	Remark                 string     `json:"remark"`
	// Version must match the stored row when updating an existing location.
	// Omit it to create.
	Version *int `json:"version,omitempty"`
	// Force allows shrinking capacity below the stock currently held.
	Force     bool   `json:"force"`
	UpdatedBy string `json:"updatedBy"`
}

// Validate applies the rules that do not need the database.
func (in *StorageLocationInput) Validate() error {
	in.StorageCode = strings.TrimSpace(in.StorageCode)
	in.StorageName = strings.TrimSpace(in.StorageName)
	in.StorageTypeCode = strings.TrimSpace(in.StorageTypeCode)

	if in.FactoryID <= 0 {
		return fmt.Errorf("%w: factoryId is required", ErrValidation)
	}
	if in.StorageCode == "" {
		return fmt.Errorf("%w: storageCode is required", ErrValidation)
	}
	if in.StorageName == "" {
		return fmt.Errorf("%w: storageName is required", ErrValidation)
	}
	if in.StorageTypeCode == "" {
		return fmt.Errorf("%w: storageTypeCode is required", ErrValidation)
	}
	if in.PhysicalCapacity < 0 {
		return fmt.Errorf("%w: physicalCapacity cannot be negative", ErrValidation)
	}
	if in.SafeCapacityPercentage <= 0 || in.SafeCapacityPercentage > 100 {
		return fmt.Errorf("%w: safeCapacityPercentage must be greater than 0 and at most 100, got %v",
			ErrValidation, in.SafeCapacityPercentage)
	}
	if in.MinimumStockLevel < 0 {
		return fmt.Errorf("%w: minimumStockLevel cannot be negative", ErrValidation)
	}
	if in.MinimumStockLevel > in.PhysicalCapacity {
		return fmt.Errorf("%w: minimumStockLevel %v exceeds physicalCapacity %v",
			ErrValidation, in.MinimumStockLevel, in.PhysicalCapacity)
	}
	if in.EffectiveFrom != nil && in.EffectiveTo != nil && in.EffectiveTo.Before(*in.EffectiveFrom) {
		return fmt.Errorf("%w: effectiveTo is before effectiveFrom", ErrValidation)
	}

	switch in.Status {
	case "":
		in.Status = domain.StatusActive
	case domain.StatusActive, domain.StatusInactive:
	default:
		return fmt.Errorf("%w: status must be ACTIVE or INACTIVE, got %q", ErrValidation, in.Status)
	}

	if in.CapacityUOM == "" {
		in.CapacityUOM = "TON"
	}
	return nil
}

// StorageProductCapacityInput configures one cell of the
// warehouse + product + packaging matrix.
type StorageProductCapacityInput struct {
	StorageCode       string     `json:"storageCode"`
	ProductCode       string     `json:"productCode"`
	PackagingCode     string     `json:"packagingCode"`
	MaximumPackageQty *float64   `json:"maximumPackageQuantity,omitempty"`
	MaximumWeightQty  *float64   `json:"maximumWeightQuantity,omitempty"`
	MinimumStockQty   float64    `json:"minimumStockQuantity"`
	MaximumSafeQty    *float64   `json:"maximumSafeQuantity,omitempty"`
	WeightUOM         string     `json:"weightUom"`
	EffectiveFrom     *time.Time `json:"effectiveFrom,omitempty"`
	EffectiveTo       *time.Time `json:"effectiveTo,omitempty"`
	Status            string     `json:"status"`
	Remark            string     `json:"remark"`
	Version           *int       `json:"version,omitempty"`
	UpdatedBy         string     `json:"updatedBy"`
}

// Validate applies the rules that do not need the database.
func (in *StorageProductCapacityInput) Validate() error {
	in.StorageCode = strings.TrimSpace(in.StorageCode)
	in.ProductCode = strings.TrimSpace(in.ProductCode)
	in.PackagingCode = strings.TrimSpace(in.PackagingCode)

	if in.StorageCode == "" || in.ProductCode == "" || in.PackagingCode == "" {
		return fmt.Errorf("%w: storageCode, productCode and packagingCode are all required", ErrValidation)
	}
	// The schema enforces this too; catching it here gives a better message.
	if in.MaximumPackageQty == nil && in.MaximumWeightQty == nil {
		return fmt.Errorf("%w: supply maximumPackageQuantity or maximumWeightQuantity, or both", ErrValidation)
	}
	if in.MaximumPackageQty != nil && *in.MaximumPackageQty < 0 {
		return fmt.Errorf("%w: maximumPackageQuantity cannot be negative", ErrValidation)
	}
	if in.MaximumWeightQty != nil && *in.MaximumWeightQty < 0 {
		return fmt.Errorf("%w: maximumWeightQuantity cannot be negative", ErrValidation)
	}
	if in.MinimumStockQty < 0 {
		return fmt.Errorf("%w: minimumStockQuantity cannot be negative", ErrValidation)
	}
	if in.MaximumSafeQty != nil && in.MaximumWeightQty != nil && *in.MaximumSafeQty > *in.MaximumWeightQty {
		return fmt.Errorf("%w: maximumSafeQuantity %v exceeds maximumWeightQuantity %v",
			ErrValidation, *in.MaximumSafeQty, *in.MaximumWeightQty)
	}
	if in.EffectiveFrom != nil && in.EffectiveTo != nil && in.EffectiveTo.Before(*in.EffectiveFrom) {
		return fmt.Errorf("%w: effectiveTo is before effectiveFrom", ErrValidation)
	}

	switch in.Status {
	case "":
		in.Status = domain.StatusActive
	case domain.StatusActive, domain.StatusInactive:
	default:
		return fmt.Errorf("%w: status must be ACTIVE or INACTIVE, got %q", ErrValidation, in.Status)
	}

	if in.WeightUOM == "" {
		in.WeightUOM = "TON"
	}
	return nil
}

// PackagingTypeInput maintains the packaging master.
//
// WeightInTon is always derived from NetWeight and its unit rather than taken
// from the caller: it is the single source for every bag/ton conversion in the
// system, so it must not be possible to save one that disagrees with the net
// weight beside it.
type PackagingTypeInput struct {
	Code         string  `json:"code"`
	Description  string  `json:"description"`
	NetWeight    float64 `json:"netWeight"`
	NetWeightUOM string  `json:"netWeightUom"`
	IsBulk       bool    `json:"isBulk"`
	Status       string  `json:"status"`
	Version      *int    `json:"version,omitempty"`
	UpdatedBy    string  `json:"updatedBy"`

	// weightInTon is computed by Validate.
	weightInTon float64
}

// WeightInTon is the derived conversion factor, valid after Validate.
func (in *PackagingTypeInput) WeightInTon() float64 { return in.weightInTon }

// Validate applies the rules that do not need the database.
func (in *PackagingTypeInput) Validate() error {
	in.Code = strings.TrimSpace(in.Code)
	in.Description = strings.TrimSpace(in.Description)
	in.NetWeightUOM = strings.ToUpper(strings.TrimSpace(in.NetWeightUOM))

	if in.Code == "" {
		return fmt.Errorf("%w: code is required", ErrValidation)
	}
	if in.Description == "" {
		return fmt.Errorf("%w: description is required", ErrValidation)
	}
	if in.NetWeight < 0 {
		return fmt.Errorf("%w: netWeight cannot be negative", ErrValidation)
	}

	if in.NetWeightUOM == "" {
		in.NetWeightUOM = "KG"
	}
	switch in.NetWeightUOM {
	case "KG":
		in.weightInTon = in.NetWeight / 1000
	case "TON":
		in.weightInTon = in.NetWeight
	default:
		return fmt.Errorf("%w: netWeightUom must be KG or TON, got %q", ErrValidation, in.NetWeightUOM)
	}

	// A non-bulk package that weighs nothing would make bags/tons undefined.
	if !in.IsBulk && in.weightInTon <= 0 {
		return fmt.Errorf("%w: a non-bulk package must have a net weight above zero", ErrValidation)
	}
	if in.IsBulk && in.weightInTon != 0 {
		return fmt.Errorf("%w: bulk packaging must have a net weight of zero", ErrValidation)
	}

	switch in.Status {
	case "":
		in.Status = domain.StatusActive
	case domain.StatusActive, domain.StatusInactive:
	default:
		return fmt.Errorf("%w: status must be ACTIVE or INACTIVE, got %q", ErrValidation, in.Status)
	}
	return nil
}

// ThresholdBandsInput replaces a factory's alert bands as one set.
//
// Bands are replaced wholesale rather than one at a time because they only
// make sense as a contiguous cover: a gap would leave a utilisation with no
// band, and an overlap would make the classification ambiguous.
type ThresholdBandsInput struct {
	// FactoryID of zero maintains the global default set.
	FactoryID int64                `json:"factoryId"`
	Bands     []ThresholdBandInput `json:"bands"`
	UpdatedBy string               `json:"updatedBy"`
}

// ThresholdBandInput is one alert band.
type ThresholdBandInput struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Severity string   `json:"severity"`
	UIState  string   `json:"uiState"`
	From     float64  `json:"fromPercentage"`
	To       *float64 `json:"toPercentage,omitempty"`
}

var validSeverities = map[string]bool{
	capacity.SeverityNormal:   true,
	capacity.SeverityWarning:  true,
	capacity.SeverityHigh:     true,
	capacity.SeverityCritical: true,
	capacity.SeverityFull:     true,
}

// Validate checks the set covers 0% upwards with no gap and no overlap.
func (in *ThresholdBandsInput) Validate() error {
	if len(in.Bands) == 0 {
		return fmt.Errorf("%w: supply at least one band", ErrValidation)
	}

	seen := map[string]bool{}
	for i := range in.Bands {
		b := &in.Bands[i]
		b.Code = strings.ToUpper(strings.TrimSpace(b.Code))
		b.Severity = strings.ToUpper(strings.TrimSpace(b.Severity))

		if b.Code == "" {
			return fmt.Errorf("%w: band %d has no code", ErrValidation, i+1)
		}
		if seen[b.Code] {
			return fmt.Errorf("%w: duplicate band code %q", ErrValidation, b.Code)
		}
		seen[b.Code] = true

		if b.Name == "" {
			b.Name = b.Code
		}
		if !validSeverities[b.Severity] {
			return fmt.Errorf("%w: band %q has severity %q, want one of NORMAL, WARNING, HIGH, CRITICAL, FULL",
				ErrValidation, b.Code, b.Severity)
		}
		if b.UIState == "" {
			b.UIState = "None"
		}
		if b.From < 0 {
			return fmt.Errorf("%w: band %q starts below 0%%", ErrValidation, b.Code)
		}
		if b.To != nil && *b.To <= b.From {
			return fmt.Errorf("%w: band %q ends at %v, which is not above its start of %v",
				ErrValidation, b.Code, *b.To, b.From)
		}
	}

	// Sort a copy by lower bound and check the cover.
	sorted := make([]ThresholdBandInput, len(in.Bands))
	copy(sorted, in.Bands)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].From < sorted[j-1].From; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}

	if sorted[0].From != 0 {
		return fmt.Errorf("%w: the lowest band must start at 0%%, but %q starts at %v",
			ErrValidation, sorted[0].Code, sorted[0].From)
	}
	for i := 0; i < len(sorted)-1; i++ {
		if sorted[i].To == nil {
			return fmt.Errorf("%w: band %q has no upper bound but is not the highest band",
				ErrValidation, sorted[i].Code)
		}
		if *sorted[i].To != sorted[i+1].From {
			return fmt.Errorf("%w: band %q ends at %v but %q starts at %v, leaving a gap or an overlap",
				ErrValidation, sorted[i].Code, *sorted[i].To, sorted[i+1].Code, sorted[i+1].From)
		}
	}
	if last := sorted[len(sorted)-1]; last.To != nil {
		return fmt.Errorf("%w: the highest band %q must have no upper bound so every utilisation is covered",
			ErrValidation, last.Code)
	}

	in.Bands = sorted
	return nil
}
