// Package domain holds the entities of the production planning system.
//
// Nothing here talks to a database or an HTTP handler: these are the shapes the
// rest of the application agrees on.
package domain

import "time"

// Status values shared by most master data records.
const (
	StatusActive   = "ACTIVE"
	StatusInactive = "INACTIVE"
)

// Storage type codes. New codes are data, not constants — these four exist so
// the engine can special-case weight-only storage without a lookup.
const (
	StorageTypeTank                   = "TANK"
	StorageTypeRawSugarWarehouse      = "RAW_SUGAR_WAREHOUSE"
	StorageTypeConditioningSilo       = "CONDITIONING_SILO"
	StorageTypeFinishedGoodsWarehouse = "FINISHED_GOODS_WAREHOUSE"
)

// UOM is a unit of measure.
type UOM struct {
	ID           int64   `json:"id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Dimension    string  `json:"dimension"`
	FactorToBase float64 `json:"factorToBase"`
	IsBase       bool    `json:"isBase"`
}

// Factory is a production site.
type Factory struct {
	ID          int64  `json:"id"`
	CompanyID   int64  `json:"companyId"`
	CompanyName string `json:"companyName"`
	Code        string `json:"code"`
	Name        string `json:"name"`
}

// Product is a material the factory produces or stores.
type Product struct {
	ID           int64  `json:"id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	ProductGroup string `json:"productGroup"`
	IsBulkLiquid bool   `json:"isBulkLiquid"`
	Status       string `json:"status"`
}

// PackagingType is an entry in the packaging master (requirement section 5).
//
// WeightInTon is the single source for the package <-> weight conversion; it is
// never hard-coded in Go.
type PackagingType struct {
	ID           int64   `json:"id"`
	Code         string  `json:"code"`
	Description  string  `json:"description"`
	NetWeight    float64 `json:"netWeight"`
	NetWeightUOM string  `json:"netWeightUom"`
	WeightInTon  float64 `json:"weightInTon"`
	IsBulk       bool    `json:"isBulk"`
	Status       string  `json:"status"`
	// Version supports optimistic locking on update.
	Version int `json:"version"`
}

// ProductPackaging is a product + packaging combination (requirement section 6).
type ProductPackaging struct {
	ID            int64  `json:"id"`
	ProductID     int64  `json:"productId"`
	ProductCode   string `json:"productCode"`
	ProductName   string `json:"productName"`
	PackagingID   int64  `json:"packagingTypeId"`
	PackagingCode string `json:"packagingCode"`
	SKUCode       string `json:"skuCode"`
	SKUName       string `json:"skuName"`
	IsDefault     bool   `json:"isDefault"`
	Status        string `json:"status"`
}

// StorageType classifies storage locations (requirement section 2).
type StorageType struct {
	ID             int64  `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	TracksPackages bool   `json:"tracksPackages"`
	Description    string `json:"description"`
}

// Audit is the four mandatory fields every table carries, as they are read
// back for display.
//
// It is embedded rather than repeated so that a new entity gets them by
// declaring one field, and so that a screen showing them can be written once
// against one shape. The names come from the user master through a join, not
// from a copy stored beside the ids, so renaming somebody corrects every
// screen that ever showed them.
//
// Nothing here is settable from a request. The API accepts no value for any of
// these, and the database overwrites the timestamps regardless — see
// docs/audit-fields.md.
type Audit struct {
	CreatedBy     int64     `json:"createdBy"`
	CreatedByName string    `json:"createdByName"`
	CreatedAt     time.Time `json:"createdAt"`
	ChangedBy     int64     `json:"changedBy"`
	ChangedByName string    `json:"changedByName"`
	ChangedAt     time.Time `json:"changedAt"`
}

// StorageLocation is a tank, silo or warehouse (requirement section 3).
type StorageLocation struct {
	ID                     int64      `json:"id"`
	FactoryID              int64      `json:"factoryId"`
	StorageCode            string     `json:"storageCode"`
	StorageName            string     `json:"storageName"`
	StorageTypeID          int64      `json:"storageTypeId"`
	StorageTypeCode        string     `json:"storageTypeCode"`
	StorageTypeName        string     `json:"storageTypeName"`
	TracksPackages         bool       `json:"tracksPackages"`
	PhysicalCapacity       float64    `json:"physicalCapacity"`
	CapacityUOM            string     `json:"capacityUom"`
	MinimumStockLevel      float64    `json:"minimumStockLevel"`
	SafeCapacityPercentage float64    `json:"safeCapacityPercentage"`
	AllowMixedProducts     bool       `json:"allowMixedProducts"`
	AllowMixedBatches      bool       `json:"allowMixedBatches"`
	Status                 string     `json:"status"`
	EffectiveFrom          time.Time  `json:"effectiveFrom"`
	EffectiveTo            *time.Time `json:"effectiveTo,omitempty"`
	Remark                 string     `json:"remark"`
	// Version supports optimistic locking on update.
	Version int `json:"version"`
	Audit   `json:"audit"`
}

// StorageGroup is a pool of locations planned against one shared ceiling.
type StorageGroup struct {
	ID        int64             `json:"id"`
	FactoryID int64             `json:"factoryId"`
	GroupCode string            `json:"groupCode"`
	GroupName string            `json:"groupName"`
	Remark    string            `json:"remark"`
	Members   []StorageLocation `json:"members,omitempty"`
}

// PooledCapacity is the sum of the members' physical capacity.
func (g StorageGroup) PooledCapacity() float64 {
	var total float64
	for _, m := range g.Members {
		if m.Status == StatusActive {
			total += m.PhysicalCapacity
		}
	}
	return total
}

// StorageProductCapacity is one row of the warehouse + product + packaging
// capacity matrix (requirement sections 4, 12, 22).
//
// A nil maximum means "not constrained on this axis".
type StorageProductCapacity struct {
	ID                   int64      `json:"id"`
	StorageLocationID    int64      `json:"storageLocationId"`
	StorageCode          string     `json:"storageCode"`
	StorageName          string     `json:"storageName"`
	ProductID            int64      `json:"productId"`
	ProductCode          string     `json:"productCode"`
	ProductName          string     `json:"productName"`
	PackagingTypeID      int64      `json:"packagingTypeId"`
	PackagingCode        string     `json:"packagingCode"`
	PackagingDescription string     `json:"packagingDescription"`
	WeightPerPackage     float64    `json:"weightPerPackage"`
	MaximumPackageQty    *float64   `json:"maximumPackageQuantity,omitempty"`
	MaximumWeightQty     *float64   `json:"maximumWeightQuantity,omitempty"`
	MinimumStockQty      float64    `json:"minimumStockQuantity"`
	MaximumSafeQty       *float64   `json:"maximumSafeQuantity,omitempty"`
	WeightUOM            string     `json:"weightUom"`
	EffectiveFrom        time.Time  `json:"effectiveFrom"`
	EffectiveTo          *time.Time `json:"effectiveTo,omitempty"`
	Status               string     `json:"status"`
	Remark               string     `json:"remark"`
	// Version supports optimistic locking on update.
	Version int `json:"version"`
}

// InventoryBalance is stock held for one Factory + Storage + Product +
// Packaging + Batch key (requirement section 23).
type InventoryBalance struct {
	ID                int64      `json:"id"`
	FactoryID         int64      `json:"factoryId"`
	StorageLocationID int64      `json:"storageLocationId"`
	StorageCode       string     `json:"storageCode"`
	StorageName       string     `json:"storageName"`
	ProductID         int64      `json:"productId"`
	ProductCode       string     `json:"productCode"`
	ProductName       string     `json:"productName"`
	PackagingTypeID   int64      `json:"packagingTypeId"`
	PackagingCode     string     `json:"packagingCode"`
	BatchNo           string     `json:"batchNo"`
	PackageQty        float64    `json:"packageQuantity"`
	WeightQty         float64    `json:"weightQuantity"`
	ReservedPackage   float64    `json:"reservedPackageQuantity"`
	ReservedWeight    float64    `json:"reservedWeightQuantity"`
	WeightUOM         string     `json:"weightUom"`
	LastMovementDate  *time.Time `json:"lastMovementDate,omitempty"`
}

// AvailablePackages is on-hand less reserved (requirement section 9).
func (b InventoryBalance) AvailablePackages() float64 { return b.PackageQty - b.ReservedPackage }

// AvailableWeight is on-hand less reserved.
func (b InventoryBalance) AvailableWeight() float64 { return b.WeightQty - b.ReservedWeight }

// MovementType classifies an inventory movement.
type MovementType struct {
	ID                    int64  `json:"id"`
	Code                  string `json:"code"`
	Name                  string `json:"name"`
	Direction             string `json:"direction"`
	RequiresCapacityCheck bool   `json:"requiresCapacityCheck"`
}

// Movement directions.
const (
	DirectionIn     = "IN"
	DirectionOut    = "OUT"
	DirectionAdjust = "ADJUST"
)

// InventoryMovement is one line of the stock ledger. Quantities are signed:
// positive increases stock, negative decreases it.
type InventoryMovement struct {
	ID                     int64      `json:"id"`
	FactoryID              int64      `json:"factoryId"`
	MovementDate           time.Time  `json:"movementDate"`
	MovementTypeID         int64      `json:"movementTypeId"`
	MovementTypeCode       string     `json:"movementTypeCode"`
	Direction              string     `json:"direction"`
	StorageLocationID      int64      `json:"storageLocationId"`
	StorageCode            string     `json:"storageCode"`
	ProductID              int64      `json:"productId"`
	ProductCode            string     `json:"productCode"`
	PackagingTypeID        int64      `json:"packagingTypeId"`
	PackagingCode          string     `json:"packagingCode"`
	BatchNo                string     `json:"batchNo"`
	PackageQty             float64    `json:"packageQuantity"`
	WeightQty              float64    `json:"weightQuantity"`
	WeightUOM              string     `json:"weightUom"`
	TransferRef            string     `json:"transferRef"`
	ReferenceDoc           string     `json:"referenceDoc"`
	Remark                 string     `json:"remark"`
	Posted                 bool       `json:"posted"`
	PostedAt               *time.Time `json:"postedAt,omitempty"`
	PostedBy               string     `json:"postedBy,omitempty"`
	CapacityOverride       bool       `json:"capacityOverride"`
	CapacityOverrideReason string     `json:"capacityOverrideReason,omitempty"`
}

// User is an application account (see migration 0007).
//
// The password digest is deliberately absent: it is read by the login path
// alone, and a struct that never carries it cannot leak it through a JSON
// response.
type User struct {
	ID          int64    `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email"`
	Roles       []string `json:"roles"`
	Status      string   `json:"status"`
	// MustChangePassword is set when an administrator issued the password.
	MustChangePassword bool `json:"mustChangePassword"`
	// CanSignIn reports whether the account has a password at all. An account
	// without one still owns its history; it simply cannot log in.
	CanSignIn bool `json:"canSignIn"`
	// IsDemo marks an account whose password is published in the project
	// documentation. It exists so the system can say so out loud.
	IsDemo            bool       `json:"isDemo"`
	FailedAttempts    int        `json:"failedAttempts"`
	LockedUntil       *time.Time `json:"lockedUntil,omitempty"`
	LastLoginAt       *time.Time `json:"lastLoginAt,omitempty"`
	PasswordChangedAt *time.Time `json:"passwordChangedAt,omitempty"`
	Remark            string     `json:"remark"`
	CreatedAt         time.Time  `json:"createdAt"`
	// Version supports optimistic locking on update.
	Version int `json:"version"`
}

// Locked reports whether sign-in is being throttled at the given time.
func (u User) Locked(now time.Time) bool {
	return u.LockedUntil != nil && u.LockedUntil.After(now)
}

// UserSession is one signed-in browser or API client.
//
// The token itself is not here for the same reason the password digest is not
// on User: only its hash is stored, and nothing outside the login path needs
// even that.
type UserSession struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userId"`
	Username   string     `json:"username,omitempty"`
	IssuedAt   time.Time  `json:"issuedAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastSeenAt time.Time  `json:"lastSeenAt"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	UserAgent  string     `json:"userAgent,omitempty"`
	ClientIP   string     `json:"clientIp,omitempty"`
}

// Season is a crushing campaign.
type Season struct {
	ID            int64     `json:"id"`
	FactoryID     int64     `json:"factoryId"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	CrushingStart time.Time `json:"crushingStart"`
	CrushingEnd   time.Time `json:"crushingEnd"`
	SeasonEnd     time.Time `json:"seasonEnd"`
	CaneTarget    float64   `json:"caneTarget"`
	RecoveryRate  float64   `json:"recoveryRate"`
	Status        string    `json:"status"`
}

// DailyProductionPlan is one day of the production plan.
type DailyProductionPlan struct {
	ID                    int64     `json:"id"`
	SeasonID              int64     `json:"seasonId"`
	FactoryID             int64     `json:"factoryId"`
	PlanDate              time.Time `json:"planDate"`
	DayNo                 *int      `json:"dayNo,omitempty"`
	CaneTarget            float64   `json:"caneTarget"`
	CaneActual            float64   `json:"caneActual"`
	RawSugarTarget        float64   `json:"rawSugarTarget"`
	RawSugarActual        float64   `json:"rawSugarActual"`
	RawToRemeltTarget     float64   `json:"rawToRemeltTarget"`
	RawSiloToRemeltTarget float64   `json:"rawSiloToRemeltTarget"`
	RawPackingTarget      float64   `json:"rawPackingTarget"`
	RefinedSugarTarget    float64   `json:"refinedSugarTarget"`
	WhiteSugarTarget      float64   `json:"whiteSugarTarget"`
	SuperRefinedTarget    float64   `json:"superRefinedTarget"`
	QuotaSalesTarget      float64   `json:"quotaSalesTarget"`
	IsCleaningDay         bool      `json:"isCleaningDay"`
	Remark                string    `json:"remark"`
}

// DailyStoragePlan is one planned day for one storage scope
// (requirement section 15).
type DailyStoragePlan struct {
	ID                   int64     `json:"id"`
	FactoryID            int64     `json:"factoryId"`
	PlanDate             time.Time `json:"planDate"`
	StorageLocationID    *int64    `json:"storageLocationId,omitempty"`
	StorageGroupID       *int64    `json:"storageGroupId,omitempty"`
	ScopeCode            string    `json:"scopeCode"`
	ScopeName            string    `json:"scopeName"`
	ProductID            *int64    `json:"productId,omitempty"`
	ProductCode          string    `json:"productCode,omitempty"`
	PackagingTypeID      *int64    `json:"packagingTypeId,omitempty"`
	PackagingCode        string    `json:"packagingCode,omitempty"`
	OpeningWeight        float64   `json:"openingWeight"`
	PlannedInWeight      float64   `json:"plannedInWeight"`
	PlannedOutWeight     float64   `json:"plannedOutWeight"`
	PlannedClosingWeight float64   `json:"plannedClosingWeight"`
	OpeningPackages      float64   `json:"openingPackages"`
	PlannedInPackages    float64   `json:"plannedInPackages"`
	PlannedOutPackages   float64   `json:"plannedOutPackages"`
	PlannedClosingPkgs   float64   `json:"plannedClosingPackages"`
	WeightUOM            string    `json:"weightUom"`
	Status               string    `json:"status"`
	Remark               string    `json:"remark"`
}
