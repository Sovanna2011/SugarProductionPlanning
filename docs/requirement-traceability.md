# Requirement traceability

Maps each section of
[`docs/source/requirement-storage-capacity-management.md`](source/requirement-storage-capacity-management.md)
to where it is implemented.

| § | Requirement | Implementation |
|---|---|---|
| 1 | Storage structure | `0005_seed_master_data.sql` — 3 tanks, 2 raw warehouses, 1 conditioning silo, 3 finished warehouses |
| 2 | Storage location types | `storage_types` table + seed |
| 3 | Storage location master | `storage_locations` — all listed fields including safe %, mixed products/batches, effective dates, remark |
| 4 | Product-specific storage capacity | `storage_product_capacity` keyed by location + product + packaging |
| 5 | Packaging master | `packaging_types`; six sizes seeded, extensible by `INSERT`. No package size appears in Go |
| 6 | Product + packaging combination | `product_packaging`; inventory keyed by product + packaging + batch + location |
| 7 | Molasses tank capacity | Tank rows + `capacity.Utilize`; available and utilisation formulas covered by `TestUtilizeMatchesTheTankExample` |
| 8 | Raw sugar warehouse capacity | `RAW-WH01/02` with bulk and jumbo capacity rows |
| 9 | Raw sugar → remelt availability | `inventory_available` view, `GET /inventory/balances`; issue blocked when stock is short |
| 10 | Conditioning Silo 1 | `CON-S01`, storage type `tracks_packages = false`, weight-managed |
| 11 | Finished sugar warehouses | `FG-WH01/02/03`; new products need only a new capacity row |
| 12 | Finished warehouse product capacity | Seeded capacity matrix, all values master data |
| 13 | Shared warehouse capacity | `capacity.Validate` checks the product ceiling **and** the location total; `TestValidateBlocksOnPhysicalCapacityEvenWhenTheProductFits` |
| 14 | Package/weight conversion | `capacity.WeightOf` / `PackagesOf`; the API derives whichever axis the caller omits |
| 15 | Daily warehouse planning | `daily_storage_plans`; `GET /planning/storage` |
| 16 | Daily warehouse actual | Balances only change via posted movements, in the same transaction. No API writes a closing stock |
| 17 | Plan vs actual | `GET /planning/plan-vs-actual` — opening, planned/actual in and out, closing, variance, capacity, available, utilisation |
| 18 | Capacity alerts | `capacity_threshold_levels` (configurable bands) + `capacity.Classify`; `GET /alerts` |
| 19 | Future capacity planning | `GET /planning/projection` — daily curve, first safe and physical breach dates, horizons today/+1/+3/+7/end of month/end of season |
| 20 | Warehouse dashboard | `webapp/` UI5 app — all six filters, pooled cards, per-location utilisation bars, product tables, alerts |
| 21 | PostgreSQL storage master design | `backend/migrations/0001`–`0004`; all listed tables plus audit and version columns |
| 22 | Product capacity table | `storage_product_capacity` with every listed field |
| 23 | Inventory balance structure | `inventory_balances` unique on location + product + packaging + batch |
| 24 | Capacity validation | `capacity.Validate` — physical, product, package, safe, plus product-allowed and mixed-products. Blocking findings need an authorised override with a reason |
| 25 | Updated storage process | Modelled by storage types, groups and movement types |
| 26 | Final storage requirement | The dashboard reports physical, product and package capacity, current, reserved, available stock, available capacity, utilisation and projected capacity |

## Deviations and additions

**Storage groups** (not in the requirement). The 2026/27 workbook plans raw
sugar against the combined 110,000 t of Raw Warehouses 1 + 2, and finished
sugar against the combined 69,000 t of Finished Warehouses 1 + 3. Splitting
those into per-warehouse plans would have meant inventing a ratio, so pooled
groups are modelled explicitly and can be planned and projected like a
location.

**Missing capacity row means "not permitted"**. §24 requires validating against
a product ceiling but does not say what to do when none is configured. A
receipt for an unconfigured product/packaging is blocked, so that master data
decides what may go where. Like any block it can be overridden with a reason.

**Two remelt series kept separate.** The workbook has both raw sugar sent
straight to the refinery during crushing and raw sugar drawn back out of the
warehouses afterwards. They are different flows and get their own columns.

**Finished Sugar Warehouse 2** is seeded inactive with zero capacity: it is in
the requirement's storage structure but not in the plan, and inventing a
capacity would have corrupted the 69,000 t total the plan depends on.
