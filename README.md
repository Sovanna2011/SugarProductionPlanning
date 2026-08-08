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
    ├── service/         Dashboard, projection, plan vs actual, posting, master data
    ├── httpapi/         Routing and JSON
    └── config/          Environment configuration
webapp/                  SAP UI5 app: dashboard, master data, plan vs actual, remelt
scripts/                 Workbook extraction
docs/                    Data model, plan analysis, source documents
```

## Quick start

### The demo system

One command, from nothing to a system you can sign in to and use:

```bash
docker compose -f docker-compose.demo.yml up --build
# open http://localhost:8080
```

That brings up PostgreSQL and the application, applies the migrations, loads
the 2026/27 season plan, posts the stock position the plan itself predicts for
15 Feb 2027, and creates five accounts — one per role. Sign in as `admin`,
`planner`, `warehouse`, `refinery` or `viewer`, all with the password
`Demo-Sugar-2027`, which the login screen also lists.

Every step is safe to repeat, so restarting the container neither duplicates
the data nor fails.

On a machine with Go and PostgreSQL but no Docker:

```bash
./scripts/demo.sh                     # same result
./scripts/demo.sh --date 2027-04-10   # start from the raw sugar peak instead
```

[`docs/demo-user-test.md`](docs/demo-user-test.md) is a script for putting it
in front of the people who would use it: tasks per role, what to watch for, and
the questions the software cannot answer — starting with whether the
placeholder tank capacities are right.

**The demo accounts have a password published in this repository.** The server
says so on every start. Before the system holds anything real, run
`spp-seed-users -remove-demo` and create an administrator with
`spp-seed-users -admin <name>`.

### With Docker

```bash
docker compose up --build
# open http://localhost:8080
```

That brings up PostgreSQL and the API server with the dashboard bundled, and
seeds the database on first start with the master data and the 2026/27 season
plan. Override `SPP_DB_PASSWORD`, `SPP_PORT` and `SPP_DB_PORT` as needed.

A fresh database has the plan but no stock, so every warehouse reads 0%. To see
the dashboard with something in it:

```bash
docker compose exec app spp-seed-demo            # or, from source:
cd backend && go run ./cmd/seed-demo             # 15 Feb 2027 by default
```

The positions are the season plan's own closing figures for that date, split
across each pool's members in proportion to capacity, and posted through the
normal service so they pass the same capacity validation as any other receipt.
Pass `-date 2027-04-10` for the raw sugar peak. It refuses to run against a
database that already holds stock unless given `-force`.

#### Trying it with logins

This compose file leaves sign-in off: every screen is open and every button
works. (`docker-compose.demo.yml` above turns it on for you.) To do it by hand:

```bash
docker compose exec app spp-seed-users -demo     # or: cd backend && go run ./cmd/seed-users -demo
SPP_AUTH_MODE=local SPP_OVERRIDE_ROLE=ADMIN docker compose up -d app
```

The login screen then offers the five accounts; pick one and the screens
change with it.

| User | Roles | What changes |
|---|---|---|
| `admin` | `ADMIN` | Everything, plus the Users screen and the capacity override |
| `planner` | `PLANNER` | Master data and the plan; the remelt issue button is off |
| `warehouse` | `WAREHOUSE` | Stock movements; the master data buttons are off |
| `refinery` | `WAREHOUSE`, `PLANNER` | Both of the above |
| `viewer` | `VIEWER` | Every screen readable, every write refused |

They share the password `Demo-Sugar-2027`, which is on the login screen. That
is safe only because it is a fixture: the accounts are flagged in the database,
the server warns about them on every start, and `spp-seed-users -remove-demo`
takes them away. For a real deployment use `spp-seed-users -admin <name>`,
which generates a password, prints it once, and requires it to be changed at
first sign-in. See [docs/security.md](docs/security.md).

### Running from source

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
| `SPP_AUTH_MODE` | `none` | `none`, `local` or `proxy`. See [docs/security.md](docs/security.md) |
| `SPP_AUTH_USER_HEADER` | `X-Forwarded-User` | Identity header under `proxy` mode |
| `SPP_SESSION_TTL` | `12h` | How long a session lasts under `local` mode |
| `SPP_SESSION_COOKIE` | `spp_session` | Session cookie name under `local` mode |
| `SPP_SESSION_COOKIE_SECURE` | `false` | Mark the session cookie `Secure`. **Set this wherever the site is served over HTTPS** |
| `SPP_TRUSTED_PROXIES` | *(none)* | Comma-separated addresses or CIDR blocks whose `X-Forwarded-For` is believed. Set it behind a reverse proxy, or every client looks like the proxy |
| `SPP_LOGIN_MAX_FAILURES` | `15` | Failed sign-ins one client address may make per window. `0` disables the limit |
| `SPP_LOGIN_FAILURE_WINDOW` | `15m` | The period that limit applies over |
| `SPP_OVERRIDE_ROLE` | *(none)* | Role required to force a blocked posting. Unset leaves overrides open |

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
25 KG or 500 KG bag is a `POST /master/packaging-types`.

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
Set `SPP_OVERRIDE_ROLE` to restrict overrides to a role, which is what
section 24.5's "authorized business rule" asks for; unset, anyone may override.

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

**Signing in** (`local` mode; the first four need no session)

```
GET  /auth/config                             what this deployment does about sign-in
POST /auth/login                              {username, password} -> session cookie + token
GET  /auth/me                                 who the caller is, and what they may do
POST /auth/logout                             ends the session
POST /auth/change-password                    {currentPassword, newPassword}

Failed sign-ins are throttled per client address and answered 429 with a
Retry-After. Successes cost nothing, so a shift change is unaffected.

GET  /admin/users                   ?includeInactive       ADMIN only
POST /admin/users                             create or update an account
POST /admin/users/reset-password              issue a new password, shown once
POST /admin/users/sign-out                    end every session of an account
```

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

POST /master/storage-locations                create or update a tank, silo or warehouse
POST /master/storage-capacities               configure warehouse + product + packaging
POST /master/packaging-types                  maintain the packaging master
PUT  /master/threshold-levels                 replace a factory's alert bands
```

**Inventory**

```
GET  /inventory/balances            ?factoryId&storageCode&storageType&productId&batchNo&nonZeroOnly
GET  /inventory/movements           ?factoryId&from&to&limit
POST /inventory/movements                     post a movement
POST /inventory/movements/validate            dry run
POST /inventory/reservations                  commit stock to an order
POST /inventory/reservations/release          free reserved stock
```

**Planning and dashboard**

```
GET  /seasons                       ?factoryId
GET  /planning/production           ?factoryId&seasonId&from&to
GET  /planning/storage              ?factoryId&scope&from&to
POST /planning/storage                        save plan lines (one object or an array)
GET  /planning/projection           ?factoryId&scope&from&days&useActualOpening
GET  /planning/plan-vs-actual       ?factoryId&date
GET  /dashboard/storage-capacity    ?factoryId&date&storageType&storageCode&productCode&packagingCode
GET  /alerts                        ?factoryId&date
GET  /health
```

### Maintaining master data

Everything the requirement calls configurable is maintained through the
**Master Data** screen in the dashboard, or through the API directly — never by
editing SQL. The screen has a tab per master: storage locations, the
warehouse + product + packaging matrix, packaging types and alert bands. Omit `version` to create; supply the stored `version` to
update, and a stale one is refused with `409 Conflict` so two editors cannot
silently overwrite each other.

```bash
# A new warehouse, then what it may hold.
curl -X POST localhost:8080/api/v1/master/storage-locations \
  -H 'Content-Type: application/json' \
  -d '{"factoryId":1,"storageCode":"FG-WH04","storageName":"Finished Sugar Warehouse 4",
       "storageTypeCode":"FINISHED_GOODS_WAREHOUSE","physicalCapacity":30000,
       "safeCapacityPercentage":95,"allowMixedProducts":true}'

curl -X POST localhost:8080/api/v1/master/storage-capacities \
  -H 'Content-Type: application/json' \
  -d '{"storageCode":"FG-WH04","productCode":"WHITE-SUGAR","packagingCode":"PKG-50KG",
       "maximumPackageQuantity":400000,"maximumWeightQuantity":20000}'
```

Two rules worth knowing:

- **Package weight is derived, never trusted.** `weightInTon` is computed from
  `netWeight` and its unit, because it is the single source for every bag/ton
  conversion. A packaging row whose conversion factor disagrees with its own
  net weight cannot be saved.
- **Alert bands are replaced as a set.** They only mean anything as a
  contiguous cover from 0% upwards with an open-ended top band, so a set with a
  gap, an overlap or a bounded top band is rejected rather than half-applied.

Capacity changes are guarded but not blocked: shrinking a warehouse below the
stock inside it is refused unless you pass `"force": true`, and a saved change
that looks wrong comes back with `warnings` rather than being rejected.

### Reservations

Reserved stock stays on hand and still counts against capacity; what it changes
is **available** stock, which is what a planner may commit next.

```bash
curl -X POST localhost:8080/api/v1/inventory/reservations \
  -H 'Content-Type: application/json' \
  -d '{"factoryId":1,"storageCode":"FG-WH01","productCode":"REFINED-SUGAR",
       "packagingCode":"PKG-50KG","batchNo":"R26001","weightQuantity":2000,
       "referenceDoc":"SO-1001"}'
```

Reserving more than is available, or releasing more than is reserved, is
refused. The balance row is locked for the duration, so two concurrent
reservations cannot both take the same free stock.

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

> **Before deploying this outside a trusted network, read
> [docs/security.md](docs/security.md).** Authentication defaults to `none`, so
> an unconfigured deployment is open. Set `SPP_AUTH_MODE=local` and create an
> administrator with `spp-seed-users`, or `SPP_AUTH_MODE=proxy` behind an
> authenticating reverse proxy — and in that case pass the roles header, or
> everyone will be able to read everything and change nothing.

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

A further 30 unit tests cover the master data validation rules — package weight
derivation, the alert band cover, and every rejection path on storage
locations, capacity rows and plan lines.

The login system adds another 24: the password policy and digest handling, the
session token and its stored form, resolving a session from a cookie or a
bearer header, and a table driving every role against every guarded route —
including that a viewer is refused a posting, that a warehouse hand is refused
master data, and that with authentication off none of it applies.

Integration tests are build-tagged so the default run stays hermetic:

```bash
createdb spp_test
SPP_TEST_DATABASE_URL="postgres://localhost/spp_test" \
  go test -tags=integration -count=1 ./...
```

Nineteen tests covering what the unit tests cannot: the SQL, migration
idempotency, the seeded season still reconciling with the summary report, every
plan line balancing, the capacity rules against real master data, balances
following posted movements, the master data round trip, optimistic locking
under a concurrent edit, reservations bounding available stock, and plan
openings carrying forward. They create the schema themselves and reverse every
change they make, so they can run repeatedly against the same database.

Seven of them are the login system end to end: a session issued and resolved and
revoked, a wrong password and an unknown user rejected in exactly the same
words, five failures holding the account and the hold then lifting, a
deactivated account losing its sessions at once, a password change revoking
every session including the one that made it, the last administrator unable to
demote themselves — and the whole HTTP stack refusing and permitting the right
things per role.

## CI

`.github/workflows/ci.yml` runs on every pull request:

| Job | What it checks |
|---|---|
| **backend** | `gofmt`, `go vet` (including the integration build), `go test -race`, `go build` |
| **integration** | Migrations against a real PostgreSQL 16, applied twice to prove idempotency, then the tagged tests |
| **seed-drift** | Regenerates the season seed from the workbook and fails if the committed file differs |
| **docker** | Builds the image and runs it against PostgreSQL: migrations apply, the dashboard is served, and with `SPP_AUTH_MODE=local` an anonymous call is refused while a signed-in viewer reads and is refused a posting |
| **webapp** | `npm ci`, both build variants, and that the bundled build can actually bootstrap |

The seed-drift job is the one worth understanding: the generator asserts every
season total against the summary report, so it fails if the workbook and the
committed seed ever diverge.
