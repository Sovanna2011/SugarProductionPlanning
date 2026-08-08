# Data model

PostgreSQL schema for storage capacity management. Every table carries
`created_at/by`, `updated_at/by` and `version` for audit and optimistic
locking; those columns are omitted below for readability.

## Design rules

1. **Anything a business user changes is a row, not code.** Package sizes,
   capacities, alert thresholds and storage types are all master data. Adding a
   500 KG bag, a fourth molasses tank or a new alert band is an `INSERT`.
2. **Capacity is two-dimensional.** Bags and tons are both tracked, and the
   conversion between them comes from `packaging_types.weight_in_ton`.
3. **Balances are derived.** Nothing writes `inventory_balances` except a posted
   movement, in the same transaction.

## Master data

### `uoms`
Units of measure. `dimension` (`WEIGHT` / `COUNT` / `VOLUME`) stops the engine
comparing bags with tons; `factor_to_base` converts to the dimension's base
unit. A partial unique index enforces one base unit per dimension.

### `companies`, `factories`
Organisation. Every storage location and plan line belongs to a factory.

### `products`
`product_group` is one of `MOLASSES`, `RAW_SUGAR`, `FINISHED_SUGAR`,
`BY_PRODUCT`.

### `packaging_types`
The packaging master (requirement §5).

| Column | Notes |
|---|---|
| `net_weight`, `net_weight_uom_id` | The package content as entered, e.g. 1,100 KG |
| `weight_in_ton` | The same figure in tons — the single source for conversion |
| `is_bulk` | Bulk storage has no discrete package count |

A check constraint requires a non-bulk package to weigh something, so
bags ↔ tons can never divide by zero.

Seeded: `PKG-BULK`, `PKG-25KG`, `PKG-50KG`, `PKG-500KG`, `PKG-1000KG`,
`PKG-JUMBO` (1.1 t).

### `product_packaging`
Which packaging each product may use (requirement §6), with an SKU code. A
partial unique index allows one default packaging per product.

## Storage

### `storage_types`
`TANK`, `RAW_SUGAR_WAREHOUSE`, `CONDITIONING_SILO`,
`FINISHED_GOODS_WAREHOUSE`. `tracks_packages` is false for tanks and the
conditioning silo, which hold unpacked product and are managed by weight
(requirement §10).

### `storage_locations`
One row per tank, silo or warehouse.

| Column | Notes |
|---|---|
| `physical_capacity`, `capacity_uom_id` | The hard ceiling |
| `safe_capacity_percentage` | Share usable under normal operation |
| `minimum_stock_level` | Lower bound |
| `allow_mixed_products`, `allow_mixed_batches` | Enforced before posting |
| `effective_from`, `effective_to` | Validity window |

### `storage_groups`, `storage_group_members`
A named pool of locations sharing one planning line and one ceiling. The
2026/27 workbook plans raw sugar against the combined 110,000 t of Raw
Warehouses 1 + 2, and finished sugar against the combined 69,000 t of Finished
Warehouses 1 + 3, so those pools are modelled directly rather than being split
with invented ratios.

A pool inherits the tightest `safe_capacity_percentage` of its active members,
so it never looks safer than the strictest warehouse in it.

### `storage_product_capacity`
The warehouse + product + packaging matrix (requirement §§4, 12, 22). One row
per combination, which is what lets a new product/packaging pairing be
configured without a schema change.

| Column | Notes |
|---|---|
| `maximum_package_quantity` | Bag ceiling; `NULL` means unconstrained |
| `maximum_weight_quantity` | Weight ceiling; `NULL` means unconstrained |
| `maximum_safe_quantity` | Optional product-level safe ceiling |
| `effective_from`, `effective_to` | Validity window |

A check constraint requires at least one of the two maxima, so a row can never
be a no-op. **The absence of a row means the product is not permitted in that
location**, and a receipt for it is blocked.

### `capacity_threshold_levels`
Configurable alert bands (requirement §18) as half-open ranges
`[from_percentage, to_percentage)`, the top band leaving `to_percentage` NULL.
`factory_id IS NULL` is the global default set; a factory-specific set
overrides it. Seeded as Normal < 80, Warning 80–90, High 90–95,
Critical 95–100, Full ≥ 100.

## Inventory

### `movement_types`
`requires_capacity_check` marks the receipts that must pass validation before
posting: production receipt, packing receipt, transfer in, tank receipt,
warehouse receipt, customer return and adjustment.

### `inventory_movements`
The ledger. Quantities are **signed** — positive increases stock, negative
decreases it — and both axes are always stored. `capacity_override` and
`capacity_override_reason` record a consciously forced posting; a check
constraint requires the reason when the flag is set.

### `inventory_balances`
Running totals keyed by **storage location + product + packaging + batch**
(requirement §23), with reserved quantities so available stock can be reported
for remelt and sales (requirement §9). The `inventory_available` view exposes
on-hand less reserved on both axes.

## Planning

### `seasons`
A crushing campaign: crushing start and end plus `season_end`, since remelt
processing continues after crushing stops. For 2026/27 that is
1 Dec 2026 → 16 Apr 2027 → 2 Sep 2027.

### `daily_production_plans`
One row per calendar day with target and actual columns for cane, raw sugar,
refined, white and super refined sugar, plus quota sales.

Two remelt series are kept apart because they are genuinely different flows:

- `raw_to_remelt_target` — fresh raw sugar sent straight to the refinery during
  crushing (124,950 t over the season).
- `raw_silo_to_remelt_target` — raw sugar drawn back **out of the warehouses**
  into the refinery, which is what empties the pool after crushing ends
  (128,047.5 t).

### `daily_storage_plans`
The daily storage plan (requirement §15). A line targets **either** a storage
location **or** a storage group, enforced by a check constraint; product and
packaging are optional, since the workbook plans pools without splitting them.

```
Planned Closing = Planned Opening + Planned In − Planned Out
```

Both weight and package axes are carried. A unique index on
`(plan_date, COALESCE(location,0), COALESCE(group,0), COALESCE(product,0), COALESCE(packaging,0))`
keeps one line per scope per day while tolerating the optional axes.

## Relationships

```
companies ─< factories ─< storage_locations >─ storage_types
                       │         │
                       │         ├─< storage_product_capacity >─ products
                       │         │                            >─ packaging_types
                       │         ├─< inventory_balances
                       │         └─< inventory_movements >─ movement_types
                       │
                       ├─< storage_groups ─< storage_group_members >─ storage_locations
                       │
                       └─< seasons ─< daily_production_plans
                                   └─< daily_storage_plans

products ─< product_packaging >─ packaging_types
```
