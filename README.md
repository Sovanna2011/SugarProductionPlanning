# Sugar Factory Production Planning

Production planning and storage capacity management for **Kampong Speu Sugar Co., Ltd.**

The system tracks what the factory produces each day and whether there is
anywhere to put it: molasses tanks, raw sugar warehouses, the conditioning silo
and the finished sugar warehouses, each with capacity managed by
**warehouse + product + packaging type**.

It is seeded with the real 2026/27 crushing season plan (2.3 million tons of
cane at 11.00% recovery) taken from the corrected planning workbook.

```
                         PRODUCTION
                             │
       ┌─────────────────────┼─────────────────────────┐
       ↓                     ↓                         ↓
   MOLASSES              RAW SUGAR              FINISHED SUGAR
       │                     │                         │
       ↓                ┌────┴────┐             ┌──────┴──────┐
   Tank 1/2/3       Raw WH1    Raw WH2       Refined        White
       │                └────┬────┘             ↓             ↓
       ↓                     ↓            Conditioning     Packing
      SALE                REMELT             Silo 1           │
                                                │             ↓
                                                ↓        FG Warehouse
                                             Packing       1 / 2 / 3
                                                │             │
                                                └──────┬──────┘
                                                       ↓
                                                      SALE
```

## Architecture

| Layer | Technology | Location |
|---|---|---|
| Database | PostgreSQL 16 | `backend/migrations/` |
| Backend | Go 1.24, `pgx`, standard-library HTTP | `backend/` |
| Frontend | SAP UI5 (OpenUI5 1.120) | `webapp/` |

The capacity rules live in `backend/internal/capacity`, a package with no
database or HTTP dependencies. It takes master data and quantities and returns
results, which is why it is covered by unit tests that assert against the real
season figures.

```
backend/
├── cmd/server/          API server, optionally serving the UI5 app
├── cmd/migrate/         Applies migrations and exits
├── migrations/          Schema and seed data (embedded in the binary)
└── internal/
    ├── domain/          Entities
    ├── capacity/        Conversion, validation, utilisation, projection  ← the rules
    ├── store/postgres/  Queries
    ├── service/         Dashboard, projection, plan vs actual, posting
    ├── httpapi/         Routing and JSON
    └── config/          Environment configuration
webapp/                  SAP UI5 Storage Capacity Dashboard
scripts/                 Workbook extraction
docs/                    Data model, plan analysis, source documents
```

## Quick start

### 1. Database

```bash
createdb spp
export SPP_DATABASE_URL="postgres://user:password@localhost:5432/spp"
```

### 2. Backend

```bash
cd backend
go test ./...                    # run the capacity engine tests
go run ./cmd/migrate             # apply schema + seed data
go run ./cmd/server              # listens on :8080
```

Migrations are embedded in the binary and tracked in `schema_migrations`, so
starting the server against an up-to-date database is a no-op. Set
`SPP_AUTO_MIGRATE=false` to migrate separately in a deployment pipeline.

### 3. Dashboard

```bash
npm install
npm start                        # ui5 serve on :8080, API via ?api=
```

For a single-process setup, build the app and point the server at it:

```bash
npm run build
SPP_WEB_DIR=$PWD/dist go run ./backend/cmd/server
# open http://localhost:8080
```

`npm run build` produces a self-contained `dist/` including the OpenUI5 runtime,
so the dashboard needs no CDN access. Use `npm run build:app` for an app-only
build when deploying against an existing SAPUI5 runtime served at `/resources`,
or change the bootstrap `src` in `webapp/index.html` to use the SAP CDN.

### Configuration

| Variable | Default | Purpose |
|---|---|---|
| `SPP_DATABASE_URL` | *(required)* | PostgreSQL connection string |
| `SPP_ADDR` | `:8080` | Listen address |
| `SPP_WEB_DIR` | *(none)* | Directory to serve the dashboard from |
| `SPP_AUTO_MIGRATE` | `true` | Apply pending migrations on start |
| `SPP_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## The capacity rules

### Package and weight are always both maintained

Inventory carries a package count and a weight. Supply either one to the API and
the other is derived from the packaging master:

```
Total Weight = Package Quantity × Net Weight per Package

100,000 bags × 50 KG  = 5,000 tons
  5,000 jumbo × 1.1 t = 5,500 tons
```

Package sizes are rows in `packaging_types`, never constants in Go. Adding a
25 KG or 500 KG bag is an `INSERT`.

### Product limits are not additive capacity

A warehouse's product ceilings say what *may* be stored, not how much the
building holds. Every receipt is validated against both:

```
Finished Sugar Warehouse 1        physical capacity 22,000 t
  Refined 50 KG   max 200,000 bags (10,000 t)
  Refined Jumbo   max   5,000 bags ( 5,500 t)
  White 50 KG     max 100,000 bags ( 5,000 t)
```

A receipt of refined sugar that fits its own 10,000 t line is still blocked if
the building is already full of white sugar.

### Pre-posting validation

`POST /api/v1/inventory/movements` runs the checks before writing anything:

| Check | Outcome |
|---|---|
| Physical capacity exceeded | **Block** |
| Product ceiling exceeded | **Block** |
| Package ceiling exceeded | **Block** |
| Product not configured for this location | **Block** |
| Mixed products where not allowed | **Block** |
| Safe capacity exceeded (still under physical) | Warning, posts normally |

A blocked posting can be forced with `"override": true` and a reason, which is
stored on the movement for audit. An override without a reason is rejected.

Use `POST /api/v1/inventory/movements/validate` to run the same checks as a dry
run.

### Actual stock is never keyed in

```
Actual Closing Stock = Opening + Actual In − Actual Out ± Adjustments
```

Balances are only ever updated by posted movements, in the same transaction that
writes the ledger line.

### Forward projection

`GET /api/v1/planning/projection?scope=FG-POOL` rolls an opening balance through
the approved daily plan and reports the first date the scope is expected to
exceed its safe and physical capacity, plus the horizons management asks for
(today, +1, +3, +7 days, end of month, end of season).

Pass `useActualOpening=true` to start from the posted balance rather than the
plan's own opening figure.

## API

All endpoints are under `/api/v1`.

**Master data**

```
GET  /factories
GET  /master/storage-types
GET  /master/storage-locations      ?factoryId&storageType&storageCode&includeInactive
GET  /master/storage-groups         ?factoryId
GET  /master/products
GET  /master/packaging-types
GET  /master/product-packaging
GET  /master/storage-capacities     ?factoryId&storageCode&productId&date
GET  /master/threshold-levels       ?factoryId
GET  /master/movement-types
```

**Inventory**

```
GET  /inventory/balances            ?factoryId&storageCode&storageType&productId&batchNo&nonZeroOnly
GET  /inventory/movements           ?factoryId&from&to&limit
POST /inventory/movements                     post a movement
POST /inventory/movements/validate            dry run
```

**Planning and dashboard**

```
GET  /seasons                       ?factoryId
GET  /planning/production           ?factoryId&seasonId&from&to
GET  /planning/storage              ?factoryId&scope&from&to
GET  /planning/projection           ?factoryId&scope&from&days&useActualOpening
GET  /planning/plan-vs-actual       ?factoryId&date
GET  /dashboard/storage-capacity    ?factoryId&date&storageType&storageCode&productCode&packagingCode
GET  /alerts                        ?factoryId&date
GET  /health
```

A scope is a storage location code (`CON-S01`) or a pooled group code
(`FG-POOL`). Projecting a scope with no daily plan returns 404.

### Example

```bash
curl -X POST localhost:8080/api/v1/inventory/movements/validate \
  -H 'Content-Type: application/json' \
  -d '{"factoryId":1,"movementType":"PACKING_RECEIPT","storageCode":"FG-WH01",
       "productCode":"REFINED-SUGAR","packagingCode":"PKG-50KG","packageQuantity":120000}'
```

```json
{
  "posted": false,
  "quantity": { "packages": 120000, "weight": 6000 },
  "validation": {
    "allowed": false,
    "requiresOverride": true,
    "findings": [
      {
        "check": "PRODUCT_CAPACITY",
        "severity": "BLOCK",
        "message": "Finished Sugar Warehouse 1 in PKG-50KG would hold 11000 TON of Refined Sugar against a product limit of 10000 TON",
        "limit": 10000, "projected": 11000, "excess": 1000, "uom": "TON"
      }
    ]
  }
}
```

## Seed data

`0005` seeds the storage structure, products, packaging and capacity matrix.
`0006` is generated from the planning workbook by

```bash
python3 scripts/generate_season_seed.py     # needs openpyxl
```

The generator refuses to write a seed that stops reconciling with the summary
report — it asserts the cane total, raw sugar production, remelt volumes, jumbo
bagging, product output, quota sales and the peak raw pool figure, and checks
that every day's stock movement balances.

Capacity figures are marked in each row's `remark`:

- **PLAN** — from the workbook and its summary report (the raw sugar and
  finished sugar warehouse capacities).
- **EXAMPLE** — the illustrative numbers from the requirement document
  (molasses tanks, the conditioning silo, the product splits). **These need
  confirming with operations.** They are ordinary master data rows.

See [docs/production-plan-2026-2027.md](docs/production-plan-2026-2027.md) for
what the plan says and what the system independently reproduces from it, and
[docs/data-model.md](docs/data-model.md) for the schema.

## Tests

```bash
cd backend
go test ./...                        # unit tests, no database needed
```

The capacity engine has 24 unit tests covering conversion, alert banding, all
five pre-posting checks and projection. Three assert against real season
figures: the finished goods pool overflowing on 28 May 2027, the requirement
document's tank example, and the shared-warehouse rule.

Integration tests are build-tagged so the default run stays hermetic:

```bash
createdb spp_test
SPP_TEST_DATABASE_URL="postgres://localhost/spp_test" \
  go test -tags=integration -count=1 ./...
```

They cover what the unit tests cannot: the SQL, migration idempotency, the
seeded season still reconciling with the summary report, every plan line
balancing, the capacity rules against real master data, and balances following
posted movements. They create the schema themselves and clean up after
themselves, so they can run repeatedly against the same database.

## CI

`.github/workflows/ci.yml` runs on every pull request:

| Job | What it checks |
|---|---|
| **backend** | `gofmt`, `go vet` (including the integration build), `go test -race`, `go build` |
| **integration** | Migrations against a real PostgreSQL 16, applied twice to prove idempotency, then the tagged tests |
| **seed-drift** | Regenerates the season seed from the workbook and fails if the committed file differs |
| **webapp** | `npm ci`, both build variants, and that the bundled build can actually bootstrap |

The seed-drift job is the one worth understanding: the generator asserts every
season total against the summary report, so it fails if the workbook and the
committed seed ever diverge.
