# Add Requirement — Warehouse, Tank, Silo & Product Capacity Management

## 1. Storage Structure

The system must manage all physical storage locations and their capacities.

Initial storage structure:

```text
FACTORY
│
├── MOLASSES STORAGE
│   ├── Tank 1
│   ├── Tank 2
│   └── Tank 3
│
├── RAW SUGAR STORAGE
│   ├── Raw Sugar Warehouse 1
│   └── Raw Sugar Warehouse 2
│
├── CONDITIONING STORAGE
│   └── Conditioning Silo 1
│
└── FINISHED SUGAR STORAGE
    ├── Finished Sugar Warehouse 1
    ├── Finished Sugar Warehouse 2
    └── Finished Sugar Warehouse 3
```

The design must be configurable so additional tanks, silos, and warehouses can be created later without database structure changes.

---

# 2. Storage Location Types

Create master data for storage types:

```text
TANK
RAW_SUGAR_WAREHOUSE
CONDITIONING_SILO
FINISHED_GOODS_WAREHOUSE
```

Example:

| Code | Storage Location | Type |
|---|---|---|
| MOL-T01 | Tank 1 | Tank |
| MOL-T02 | Tank 2 | Tank |
| MOL-T03 | Tank 3 | Tank |
| RAW-WH01 | Raw Sugar Warehouse 1 | Raw Sugar Warehouse |
| RAW-WH02 | Raw Sugar Warehouse 2 | Raw Sugar Warehouse |
| CON-S01 | Conditioning Silo 1 | Conditioning Silo |
| FG-WH01 | Finished Sugar Warehouse 1 | Finished Goods Warehouse |
| FG-WH02 | Finished Sugar Warehouse 2 | Finished Goods Warehouse |
| FG-WH03 | Finished Sugar Warehouse 3 | Finished Goods Warehouse |

---

# 3. Storage Location Master

Each storage location must contain:

```text
Storage Location ID
Storage Location Code
Storage Location Name

Storage Type

Company
Factory

Total Physical Capacity
Capacity UOM

Minimum Stock Level
Maximum Safe Capacity %

Allow Mixed Products?
Allow Mixed Batches?

Status
Effective From
Effective To

Remark
```

Example:

```text
Code: FG-WH01
Name: Finished Sugar Warehouse 1
Type: FINISHED_GOODS_WAREHOUSE

Capacity: 25,000 Tons
Maximum Safe Capacity: 95%

Mixed Products: Yes
Status: Active
```

---

# 4. Product-Specific Storage Capacity

The system must support **capacity by storage location + product + packaging type**.

This is important because the same warehouse can store different quantities depending on packaging.

Example:

```text
Finished Sugar Warehouse 1

Refined Sugar 50 KG
Capacity = 300,000 Bags

Refined Sugar Jumbo Bag
Capacity = 8,000 Jumbo Bags

White Sugar 50 KG
Capacity = 200,000 Bags

White Sugar Jumbo Bag
Capacity = 5,000 Jumbo Bags
```

Create configuration:

| Warehouse | Product | Package | Max Packages | Weight/Package | Max Weight |
|---|---|---|---:|---:|---:|
| WH1 | Refined Sugar | 50 KG | 300,000 | 0.050 T | 15,000 T |
| WH1 | Refined Sugar | Jumbo | 8,000 | 1.100 T | 8,800 T |
| WH1 | White Sugar | 50 KG | 200,000 | 0.050 T | 10,000 T |
| WH1 | White Sugar | Jumbo | 5,000 | 1.100 T | 5,500 T |

These numbers are examples only. All capacities must be configurable.

---

# 5. Packaging Master

Create a Packaging Type master.

Example:

```text
PKG-50KG
Description: 50 KG Bag
Net Weight: 50 KG
Weight in Ton: 0.050

PKG-JUMBO
Description: Jumbo Bag
Net Weight: 1,100 KG
Weight in Ton: 1.100
```

Support additional packages later:

```text
25 KG
50 KG
500 KG
1,000 KG
1,100 KG Jumbo
Bulk
```

Do not hard-code package sizes in Golang.

---

# 6. Product + Packaging Combination

Product master and packaging master must be related.

Example:

```text
REFINED SUGAR
├── Refined Sugar 50 KG
└── Refined Sugar Jumbo Bag

WHITE SUGAR
├── White Sugar 50 KG
└── White Sugar Jumbo Bag

RAW SUGAR
├── Bulk Raw Sugar
└── Raw Sugar Jumbo Bag
```

The system must identify inventory using:

```text
Product
+
Packaging Type
+
Batch
+
Storage Location
```

---

# 7. Molasses Tank Capacity

Configure separately:

```text
Tank 1
Tank 2
Tank 3
```

Each tank must have:

```text
Tank Code
Tank Name
Molasses Product
Physical Capacity
Safe Capacity
Current Stock
Available Capacity
Utilization %
Status
```

Formula:

```text
Available Capacity =
Maximum Allowed Capacity - Current Stock

Utilization % =
Current Stock / Physical Capacity × 100
```

Example:

```text
Tank 1

Physical Capacity: 5,000 Tons
Safe Capacity:     4,750 Tons
Current Stock:     4,000 Tons

Utilization:       80%
Available:         750 Tons
```

Molasses process:

```text
Molasses Production
        ↓
   ┌────┼────┐
   ↓    ↓    ↓
Tank 1 Tank 2 Tank 3
   │    │    │
   └────┼────┘
        ↓
       Sale
```

---

# 8. Raw Sugar Warehouse Capacity

Configure:

```text
Raw Sugar Warehouse 1
Raw Sugar Warehouse 2
```

Support storage by:

```text
Bulk Raw Sugar
Raw Sugar Jumbo Bag
```

Example:

```text
Raw Sugar Warehouse 1

Physical Capacity: 45,000 Tons

Bulk Raw Sugar:
30,000 Tons

Raw Sugar Jumbo:
10,000 Bags × 1.1 Ton
= 11,000 Tons
```

Raw Sugar flow:

```text
Raw Sugar Production
       ↓
┌──────┴─────────┐
↓                ↓
Warehouse 1   Warehouse 2
│                │
├── Stock        ├── Stock
├── Sale         ├── Sale
└── Remelt       └── Remelt
```

---

# 9. Raw Sugar Warehouse → Remelt

The system must show available raw sugar by warehouse.

Example:

| Warehouse | Product | Package | Stock | Reserved | Available |
|---|---|---|---:|---:|---:|
| Raw WH1 | Raw Sugar | Bulk | 20,000 T | 2,000 T | 18,000 T |
| Raw WH1 | Raw Sugar | Jumbo | 5,000 Bags | 500 Bags | 4,500 Bags |
| Raw WH2 | Raw Sugar | Bulk | 15,000 T | 1,000 T | 14,000 T |

When creating Remelt:

```text
Select Source Warehouse
        ↓
Select Product/Batch
        ↓
Check Available Stock
        ↓
Issue Raw Sugar
        ↓
Remelt
```

---

# 10. Conditioning Silo 1

Configure:

```text
Conditioning Silo 1
```

Primary product:

```text
Refined Sugar
```

Track:

```text
Physical Capacity
Maximum Safe Capacity
Opening Stock
Production Receipt
Release to Packing
Loss
Adjustment
Closing Stock
Available Capacity
Utilization %
```

Process:

```text
Refined Sugar Production
        ↓
Conditioning Silo 1
        ↓
Packing
        ↓
Refined Sugar Warehouse
```

The Conditioning Silo normally stores unpacked/bulk Refined Sugar.

Therefore its capacity should primarily be managed by weight:

```text
Ton
KG
```

rather than bag count.

---

# 11. Finished Sugar Warehouses

Configure:

```text
Finished Sugar Warehouse 1
Finished Sugar Warehouse 2
Finished Sugar Warehouse 3
```

They can store:

```text
Refined Sugar 50 KG
Refined Sugar Jumbo Bag

White Sugar 50 KG
White Sugar Jumbo Bag
```

The design must also allow future products.

---

# 12. Finished Warehouse Product Capacity

Configure capacity for each warehouse.

Example:

### Finished Sugar Warehouse 1

```text
Refined Sugar 50 KG
Maximum: 200,000 Bags

Refined Sugar Jumbo
Maximum: 5,000 Bags

White Sugar 50 KG
Maximum: 100,000 Bags
```

### Finished Sugar Warehouse 2

```text
Refined Sugar 50 KG
Maximum: 150,000 Bags

White Sugar 50 KG
Maximum: 200,000 Bags

White Sugar Jumbo
Maximum: 4,000 Bags
```

### Finished Sugar Warehouse 3

```text
Refined Sugar Jumbo
Maximum: 6,000 Bags

White Sugar Jumbo
Maximum: 6,000 Bags
```

All values must be configurable through Master Data.

---

# 13. Shared Warehouse Capacity

Product capacities must not incorrectly increase the warehouse's physical capacity.

For example:

```text
Warehouse Physical Capacity
= 20,000 Tons
```

Product configuration may say:

```text
Refined 50 KG Maximum = 300,000 Bags
White 50 KG Maximum   = 300,000 Bags
Jumbo Maximum         = 10,000 Bags
```

These are **product-specific limits**, not additive physical capacity.

The system must validate both:

```text
Product Capacity
AND
Total Warehouse Physical Capacity
```

This prevents over-allocation.

---

# 14. Package Quantity and Weight Conversion

Inventory must maintain both:

```text
Package Quantity
Weight Quantity
```

Example:

```text
Product:
Refined Sugar

Package:
50 KG

Stock:
100,000 Bags

Weight:
100,000 × 50 KG
= 5,000,000 KG
= 5,000 Tons
```

For Jumbo:

```text
5,000 Jumbo Bags
× 1.1 Ton
=
5,500 Tons
```

System formula:

```text
Total Weight =
Package Quantity × Net Weight per Package
```

---

# 15. Daily Warehouse Planning

Because the system supports daily planning, warehouse capacity must also be included in the daily plan.

Example:

| Date | Warehouse | Product | Package | Opening | Stock In | Stock Out | Closing Plan |
|---|---|---|---|---:|---:|---:|---:|
| 01-Dec | FG WH1 | Refined | 50KG | 5,000 | 500 | 200 | 5,300 T |
| 01-Dec | FG WH2 | White | 50KG | 3,000 | 400 | 300 | 3,100 T |

Formula:

```text
Planned Closing Stock =
Planned Opening Stock
+ Planned Stock In
- Planned Stock Out
```

---

# 16. Daily Warehouse Actual

Actual inventory must come from posted inventory movements.

```text
Actual Closing Stock =
Opening Stock
+ Actual Stock In
- Actual Stock Out
± Adjustments
```

Do not manually enter Actual Closing Stock.

The system calculates it automatically.

---

# 17. Daily Warehouse Plan vs Actual

For every warehouse, tank and silo show:

```text
Opening
Planned In
Actual In
Planned Out
Actual Out
Planned Closing
Actual Closing
Variance
Capacity
Available Capacity
Utilization %
```

Example:

| Storage | Product | Plan Closing | Actual Closing | Capacity | Utilization |
|---|---|---:|---:|---:|---:|
| Tank 1 | Molasses | 4,000 T | 4,100 T | 5,000 T | 82% |
| Raw WH1 | Raw Sugar | 30,000 T | 31,000 T | 45,000 T | 68.9% |
| Condition Silo 1 | Refined | 1,500 T | 1,450 T | 2,000 T | 72.5% |
| FG WH1 | Refined 50KG | 100K Bags | 105K Bags | 200K Bags | 52.5% |

---

# 18. Capacity Alerts

Generate automatic warnings.

Example thresholds:

```text
< 80%     Normal
80–89.99% Warning
90–94.99% High
>= 95%    Critical
100%      Full
```

Thresholds must be configurable.

Examples:

```text
Tank 1 is 92% full.

Raw Sugar Warehouse 1 is 96% full.

Conditioning Silo 1 has only 120 tons available.

Finished Sugar Warehouse 2 has only
15,000 spaces available for White Sugar 50 KG.
```

---

# 19. Future Capacity Planning

The system should calculate future expected capacity from daily production plans.

Example:

```text
Today:
Refined WH1 = 80% Full

Tomorrow Plan:
+1,000 Tons Production
-200 Tons Sale

Projected:
84% Full
```

Allow management to view:

```text
Today
+1 Day
+3 Days
+7 Days
End of Month
End of Season
```

based on the approved daily production and sales plan.

Highlight the first date when a warehouse/tank/silo is expected to exceed safe capacity.

---

# 20. Warehouse Dashboard

Create an SAP UI5 **Storage Capacity Dashboard**.

Filters:

```text
Factory
Date
Storage Type
Warehouse
Product
Packaging Type
```

Display:

### Molasses

```text
Tank 1     ████████░░ 80%
Tank 2     █████████░ 90%
Tank 3     ██████░░░░ 60%
```

### Raw Sugar

```text
Warehouse 1
Current: 35,000 T
Capacity: 45,000 T
Available: 10,000 T

Warehouse 2
Current: 42,000 T
Capacity: 65,000 T
Available: 23,000 T
```

### Conditioning Silo

```text
Condition Silo 1

Current: 1,500 T
Capacity: 2,000 T
Available: 500 T
Utilization: 75%
```

### Finished Sugar

```text
Warehouse 1
Refined 50KG
Refined Jumbo
White 50KG
White Jumbo

Warehouse 2
...

Warehouse 3
...
```

---

# 21. PostgreSQL Storage Master Design

Create tables such as:

```text
storage_types

storage_locations

storage_capacities

products

packaging_types

product_packaging

storage_product_capacity

inventory_movements

inventory_balances
```

Example `storage_locations`:

```text
id
factory_id
storage_code
storage_name
storage_type_id

physical_capacity
capacity_uom_id

safe_capacity_percentage

allow_mixed_products
allow_mixed_batches

status

created_at
created_by
updated_at
updated_by
version
```

---

# 22. Product Capacity Table

Create:

```text
storage_product_capacity
```

Fields:

```text
id
storage_location_id
product_id
packaging_type_id

maximum_package_quantity
maximum_weight_quantity
weight_uom_id

minimum_stock_quantity
maximum_safe_quantity

effective_from
effective_to

status
```

This table allows:

```text
WH1 + Refined + 50KG
WH1 + Refined + Jumbo

WH1 + White + 50KG
WH1 + White + Jumbo

WH2 + Refined + 50KG
...
```

without creating new database columns.

---

# 23. Inventory Balance Structure

Inventory balances must be maintained by:

```text
Factory
Storage Location
Product
Packaging Type
Batch
UOM
```

Example:

```text
FG-WH01
    │
    ├── Refined Sugar
    │      ├── 50KG
    │      │     ├── Batch R26001
    │      │     └── Batch R26002
    │      │
    │      └── Jumbo
    │            └── Batch RJ26001
    │
    └── White Sugar
           ├── 50KG
           └── Jumbo
```

---

# 24. Capacity Validation

Before posting:

```text
Production Receipt
Packing Receipt
Stock Transfer
Tank Receipt
Warehouse Receipt
Customer Return
```

the backend must check:

### 1. Available Storage Capacity

```text
Current Stock + Receipt <= Physical Capacity
```

### 2. Product Capacity

```text
Current Product Stock + Receipt
<= Product Maximum Capacity
```

### 3. Packaging Capacity

For packaged goods:

```text
Current Package Count + Incoming Packages
<= Package Capacity
```

### 4. Safe Capacity

Generate warning when projected stock exceeds safe capacity.

### 5. Physical Maximum

Block posting when physical maximum is exceeded unless an authorized business rule explicitly permits an override.

---

# 25. Updated Storage Process

The final storage structure should be:

```text
                         PRODUCTION
                             │
       ┌─────────────────────┼─────────────────────────┐
       │                     │                         │
       ↓                     ↓                         ↓
   MOLASSES              RAW SUGAR              FINISHED SUGAR
       │                     │                         │
       ↓                ┌────┴────┐             ┌──────┴──────┐
   Tank 1               ↓         ↓             ↓             ↓
   Tank 2           Raw WH1    Raw WH2       Refined        White
   Tank 3               │         │             │             │
       │                 │         │             ↓             ↓
       ↓                 └────┬────┘       Condition Silo 1  Packing
      SALE                     │                  │             │
                               ↓                  ↓             ↓
                             REMELT            Packing      FG Warehouse
                                                  │          1 / 2 / 3
                                                  ↓             │
                                            FG Warehouse       ↓
                                              1 / 2 / 3       SALE
                                                  │
                                                  ↓
                                                 SALE
```

---

# 26. Final Storage Requirement

The application must provide centralized storage management for:

**Molasses**
- Tank 1
- Tank 2
- Tank 3

**Raw Sugar**
- Raw Sugar Warehouse 1
- Raw Sugar Warehouse 2

**Refined Sugar Intermediate Storage**
- Conditioning Silo 1

**Finished Refined & White Sugar**
- Finished Sugar Warehouse 1
- Finished Sugar Warehouse 2
- Finished Sugar Warehouse 3

For finished goods, capacity must be configurable by:

**Warehouse + Product + Packaging Type**

Examples:

```text
Warehouse 1
→ Refined Sugar
   → 50 KG
   → Jumbo Bag

→ White Sugar
   → 50 KG
   → Jumbo Bag
```

The system must always know:

**Physical Capacity | Product Capacity | Package Capacity | Current Stock | Reserved Stock | Available Stock | Available Capacity | Utilization % | Projected Capacity**

and integrate these values into the **Daily Planning vs Actual** process.