# Requirement traceability

Maps each section of
[`docs/source/requirement-storage-capacity-management.md`](source/requirement-storage-capacity-management.md)
to where it is implemented.

| § | Requirement | Implementation |
|---|---|---|
| 1 | Storage structure | `0005_seed_master_data.sql` — 3 tanks, 2 raw warehouses, 1 conditioning silo, 3 finished warehouses |
| 2 | Storage location types | `storage_types` table + seed |
| 3 | Storage location master | `storage_locations` — all listed fields including safe %, mixed products/batches, effective dates, remark. Maintained via `POST /master/storage-locations` |
| 4 | Product-specific storage capacity | `storage_product_capacity` keyed by location + product + packaging. Maintained via `POST /master/storage-capacities` |
| 5 | Packaging master | `packaging_types`; six sizes seeded, extensible via `POST /master/packaging-types`. No package size appears in Go, and the ton conversion is derived from the net weight rather than trusted |
| 6 | Product + packaging combination | `product_packaging`; inventory keyed by product + packaging + batch + location |
| 7 | Molasses tank capacity | Tank rows + `capacity.Utilize`; available and utilisation formulas covered by `TestUtilizeMatchesTheTankExample` |
| 8 | Raw sugar warehouse capacity | `RAW-WH01/02` with bulk and jumbo capacity rows |
| 9 | Raw sugar → remelt availability | Guided **Issue to Remelt** screen: select warehouse → select batch → check available → issue. `POST /inventory/reservations` commits stock and bounds what is available; issue blocked when stock is short |
| 10 | Conditioning Silo 1 | `CON-S01`, storage type `tracks_packages = false`, weight-managed |
| 11 | Finished sugar warehouses | `FG-WH01/02/03`; new products need only a new capacity row |
| 12 | Finished warehouse product capacity | Seeded capacity matrix, all values maintainable through the API |
| 13 | Shared warehouse capacity | `capacity.Validate` checks the product ceiling **and** the location total; `TestValidateBlocksOnPhysicalCapacityEvenWhenTheProductFits` |
| 14 | Package/weight conversion | `capacity.WeightOf` / `PackagesOf`; the API derives whichever axis the caller omits |
| 15 | Daily warehouse planning | `daily_storage_plans`; `GET`/`POST /planning/storage`. Closing stock is derived, and an omitted opening carries the previous day's closing forward |
| 16 | Daily warehouse actual | Balances only change via posted movements, in the same transaction. No API writes a closing stock |
| 17 | Plan vs actual | **Plan vs Actual** screen over `GET /planning/plan-vs-actual` — opening, planned/actual in and out, closing, variance, capacity, available, utilisation |
| 18 | Capacity alerts | `capacity_threshold_levels` + `capacity.Classify`; `GET /alerts`. Bands are replaced as a validated contiguous set via `PUT /master/threshold-levels` |
| 19 | Future capacity planning | `GET /planning/projection` — daily curve, first safe and physical breach dates, horizons today/+1/+3/+7/end of month/end of season |
| 20 | Warehouse dashboard | `webapp/` UI5 app — all six filters, pooled cards, per-location utilisation bars, product tables, alerts |
| 21 | PostgreSQL storage master design | `backend/migrations/0001`–`0004`; all listed tables plus audit and version columns |
| 22 | Product capacity table | `storage_product_capacity` with every listed field |
| 23 | Inventory balance structure | `inventory_balances` unique on location + product + packaging + batch |
| 24 | Capacity validation | `capacity.Validate` — physical, product, package, safe, plus product-allowed and mixed-products. Blocking findings need an override with a reason, and `SPP_OVERRIDE_ROLE` restricts who may give one (§24.5) — with a login configured, that is a real authenticated identity rather than a name the caller typed |
| 25 | Updated storage process | Modelled by storage types, groups and movement types |
| 26 | Final storage requirement | The dashboard reports physical, product and package capacity, current, reserved, available stock, available capacity, utilisation and projected capacity |
| — | "All values must be configurable" (§§3, 4, 5, 12, 18, 22, 26) | Master data is maintained through the API with validation and optimistic locking, not by editing SQL |

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

**Sign-in and roles** (not in the requirement). §24.5 permits exceeding
physical capacity "unless an authorized business rule explicitly permits an
override", which presumes the system can tell who is asking. Nothing else in
the document describes users at all, so the shape is a judgement call: an
optional local login (`SPP_AUTH_MODE=local`), four roles named after jobs on
the site rather than screens in the application, and one route-to-role table
enforcing them. **With the mode left at its `none` default nothing is
enforced**, so the requirement's own behaviour is unchanged unless a deployment
opts in. See [`security.md`](security.md).

**Mandatory audit fields** (requirement update, August 2026). Every table now
carries `created_by`, `created_at`, `changed_by`, `changed_at`. The columns
already existed on eighteen of twenty-one tables under the names
`created_by`/`updated_by`, holding a username as text — so most of this was a
rename and a type change rather than an addition. Three deliberate choices are
worth knowing about before reading the migration:

- **The `_by` columns are foreign keys to `app_users`, not usernames.** Text
  cannot be joined and goes stale on a rename. Non-user actors from before the
  change — the seeder, the migrations, the test suite — map to a real `SYSTEM`
  account, and the original text is kept in `created_by_legacy` because *which*
  system actor wrote a row is worth knowing when the figure turns out to be
  wrong.
- **The database enforces it, not the application.** A `BEFORE` trigger stamps
  both timestamps from the server clock and restores `created_by`/`created_at`
  on every update, so the guarantee survives a repository that forgets, a
  hand-typed `UPDATE`, and any second application that never read the code.
- **`schema_migrations` is exempt, and the exemption is named in the guard.**
  It records the migration that creates `app_users`, so it cannot reference it.

Full detail, including the mapping from the requirement's table names onto the
ones this system actually has, is in [`audit-fields.md`](audit-fields.md).

**The central audit log** (requirement section 10). `audit_logs` records one
row per create, change or delete: which table, which record, which action, and
each field that moved with its old and new value. A trigger writes it, for the
same reason as the four fields and with more force — a log the application
maintains records exactly the writes the application remembered to record,
which is the set least likely to need auditing.

Four decisions in it are worth stating, because each one is a judgement rather
than a mechanical consequence of the requirement:

- **Secrets are recorded as having changed, never as what they changed to.**
  Hashing a password in `app_users` achieves nothing if the hash is copied into
  a table that gets exported to settle a capacity dispute.
- **A save that changed nothing writes nothing.** An entry saying a record was
  changed that cannot say how is worse than silence.
- **Three tables are excluded, and the reason is a `NOT NULL` column** on
  `audit_log_exclusions` rather than a comment, served by the API alongside the
  log. Somebody reading history and finding nothing needs to know whether
  nothing happened or nothing was recorded.
- **Reading it needs ADMIN.** Not because the entries are secret, but because
  read together they say when each person works, how fast, and what they get
  wrong — a picture of the staff rather than of the sugar. Anybody can still
  see their own actions on the record they changed.
