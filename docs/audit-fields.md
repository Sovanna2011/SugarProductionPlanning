# Mandatory audit fields

**Every table in this system carries `created_by`, `created_at`, `changed_by`
and `changed_at`. The timestamps come from the database clock. The users come
from the authenticated session. Nobody maintains any of them by hand.**

That is the whole rule. The rest of this page is how it is enforced, where it
is not, and the two distinctions that are easy to get wrong.

## The columns

| Column | Type | What it means |
|---|---|---|
| `created_by` | `BIGINT NOT NULL` → `app_users(id)` | the account that first wrote the row |
| `created_at` | `TIMESTAMPTZ NOT NULL` | when, by the database clock |
| `changed_by` | `BIGINT NOT NULL` → `app_users(id)` | the account that last changed it |
| `changed_at` | `TIMESTAMPTZ NOT NULL` | when, by the database clock |

A new table declares them like this, and the guard in
`backend/migrations/audit_test.go` fails the build if it does not:

```sql
CREATE TABLE materials (
    id            BIGSERIAL PRIMARY KEY,
    material_code VARCHAR(50)  NOT NULL UNIQUE,
    material_name VARCHAR(200) NOT NULL,
    active        BOOLEAN      NOT NULL DEFAULT TRUE,

    created_by    BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    changed_by    BIGINT      NOT NULL REFERENCES app_users (id) ON DELETE RESTRICT,
    changed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER materials_audit_stamp
    BEFORE INSERT OR UPDATE ON materials
    FOR EACH ROW EXECUTE FUNCTION audit_stamp();
```

## Why an id and not a name

`created_by` held a username as text until migration 0008. Text cannot be
joined to anything, so "everything this person touched" meant matching strings,
and it goes stale the moment somebody is renamed — leaving the old name
scattered across a million rows that no longer agree with the user master.

It is now a foreign key. The display name is resolved through the join at read
time, so correcting somebody's name corrects every screen that ever showed
them.

`ON DELETE RESTRICT` rather than `CASCADE` or `SET NULL`: deleting a user must
not delete the rows they created, and must not silently detach their name from
them either. An account that has touched anything is **deactivated**, never
removed. The constraint is what makes that the only available option rather
than the recommended one.

## Why the database enforces it

`audit_stamp()` runs `BEFORE INSERT OR UPDATE` on every audited table:

- **on insert** — `created_at` and `changed_at` are set from the transaction
  clock, whatever the caller sent;
- **on update** — `changed_at` is refreshed, and `created_by` and `created_at`
  are put back to what they were.

An application-layer convention holds until somebody writes a repository that
forgets it, or runs an `UPDATE` by hand at three in the morning to unstick a
posting. A trigger holds for both, and for the report writer, data fix or
integration that never read this file.

`now()` is the transaction clock, not the statement clock, so the two rows
written by one posting share a timestamp — which is what a reader expects of
one action.

## Why the actor cannot be forged

There is no field to forge. No request type in `internal/service` has a JSON
field for an actor; `internal/httpapi/audit_test.go` fails if one appears, in
any of the twelve spellings a client might try. The store resolves the actor
itself:

```
JWT / session cookie
        │
        ▼
authentication middleware ──▶ auth.Identity{UserID: …} in the request context
        │
        ▼
Store.ActorID(ctx) ──▶ created_by / changed_by
```

So a body like

```json
{ "storageCode": "FG-WH04", "createdBy": 999, "changedBy": 999 }
```

does not have its audit fields stripped — it never had any. The two extra keys
decode into nothing.

A request with no identity at all is attributed to the **SYSTEM** account, not
to a guess. SYSTEM is a real row in `app_users` with no password hash and an
`INACTIVE` status: it cannot be signed in to, it can be joined to, and a screen
saying "Created by System" is telling the truth. Migrations, seeders and
scheduled work land there, and so does every write under
`SPP_AUTH_MODE=none` — which is the development mode, where the server
genuinely does not know who is calling.

## The one exemption

`schema_migrations`, and the reason is written into the guard so it cannot be
quietly extended. It records which migration has run — including the one that
creates `app_users` — so a foreign key from it to the user table cannot be
satisfied at the moment it is first written. It carries `applied_at`, which is
the same information.

The guard fails if a second exemption is added without a reason, and warns if
the list grows past two. Past that it is not an exception, it is a second
convention.

## Business date is not system date

These are different, and conflating them is the most common way an audit trail
becomes useless.

| | |
|---|---|
| `posting_date`, `plan_date`, `movement_date`, `season_end` | **business dates**. Chosen by a person, according to business rules. Backdating one is legitimate. |
| `created_at`, `changed_at` | **system timestamps**. From the database clock. Nobody chooses them. |

A production movement posted on the 8th for work done on the 7th is correct
and normal:

```
Movement date : 2026-08-07     ← the day the sugar was made
Created at    : 2026-08-08 23:49:30   ← the day somebody typed it in
Created by    : SOVANNA
```

If those were the same field, "we posted this late" and "we made this late"
would be indistinguishable.

## The business timezone

Audit timestamps are `TIMESTAMPTZ`, so they are unambiguous instants and carry
no zone of their own. But *which day* an instant belongs to has to be answered
somewhere, and answering it in the reader's browser means a posting made at
06:30 in Kampong Speu lands on the previous day for anyone reading from
Europe — with both screens showing a correct time and disagreeing about the
day's production.

So the zone is configuration, in `system_parameters`:

```
BUSINESS_TIMEZONE = Asia/Phnom_Penh
```

served by `GET /api/v1/system/config` along with the server's own clock, and
used by every audit timestamp on every screen. The clock is served too so that
a factory PC with a wrong clock — and shared machines often have one — cannot
leave somebody believing a posting happened at a time it did not.

It lives in a table rather than an environment variable because it is a fact
about the factory, not about the deployment. A second server, a move to another
host, or a container rebuilt without the variable must not change which day a
posting belongs to.

## What the screens show

Every object page carries a read-only **Administrative Information** panel
(`webapp/view/AdminInfo.fragment.xml`):

```
Administrative Information
  Created By : SOVANNA
  Created At : 08 Aug 2026 23:49:30
  Changed By : ADMIN
  Changed At : 09 Aug 2026 08:15:42
  Times are shown in the factory's timezone: Asia/Phnom_Penh
```

They are `Text` controls, not disabled `Input`s. There is no field to re-enable
in the developer tools and nothing to send back — the save handler builds its
payload field by field and the audit block is not one of the fields it names.
The server would ignore it anyway and the database would overwrite it after
that, but a screen that *looks* editable invites the attempt and then has to
explain the refusal.

The panel is hidden on a new record, where four blanks would read as something
having failed to load.

## These fields are not the audit log

They answer *who last changed this, and when*. They do not answer *what
changed*.

| | The four fields | The audit log |
|---|---|---|
| Where | every table | one central table |
| Answers | who and when | who, when, what table, which record, which action, old value, new value |
| Cost | four columns | a row per change |
| Keeps | the current state of authorship | the history |

A central audit log is **specified but not yet built** — it is the natural next
piece of work, and it does not change anything on this page. What exists today
gets you "ADMIN changed this warehouse's capacity on 9 August". What it does
not get you is "from 20,000 t to 22,000 t", and for a capacity figure that
somebody will eventually dispute, the second half is the half that matters.

## Where this applies

Master Data · Season Management · Production Planning · Planning Versions ·
Daily Planning · Actual Production · Inventory · Warehouse · Tank ·
Conditioning Silo · Stock Transfer · Sales and Dispatch · Planning vs Actual ·
Reporting Configuration · User Management · Roles and Permissions · System
Configuration · Audit Log.

That is to say: everywhere. The table below maps the requirement's example
names onto the tables this system actually has, because the names differ and a
reader checking coverage should not have to guess.

| Requirement names | This system |
|---|---|
| `users`, `roles`, `permissions`, `user_roles`, `role_permissions` | `app_users` (roles are an array on the account, not a join table — a short closed list checked on every request, where a join would buy nothing) |
| `seasons` | `seasons` |
| `materials`, `units_of_measure` | `products`, `packaging_types`, `product_packaging`, `uoms` |
| `warehouses`, `warehouse_materials` | `storage_locations`, `storage_types`, `storage_groups`, `storage_group_members`, `storage_product_capacity` |
| `planning_versions`, `daily_production_plans`, `daily_plan_details` | `daily_production_plans`, `daily_storage_plans` (versions not yet built) |
| `production_documents`, `production_document_items` | not yet built; actuals are derived from `inventory_movements` |
| `inventory_movements`, `inventory_balances` | same names |
| `stock_transfers`, `stock_transfer_items` | `inventory_movements` with a transfer movement type |
| `sales_dispatches`, `sales_dispatch_items` | `inventory_movements` with a sales movement type |
| `audit_logs` | not yet built — see above |
| `system_parameters` | `system_parameters` |

Tables that do not exist yet will carry the four fields when they are written,
because the guard will not let them merge without.

## Checking it

```bash
# No database needed. Fails on a new table that skips the standard.
go test ./migrations/

# Against a real schema: columns, types, nullability, foreign keys, triggers,
# and the four guarantees exercised rather than inspected.
go test -tags=integration ./internal/store/postgres/ -run Audit

# That no request type anywhere accepts an actor from JSON.
go test ./internal/httpapi/ -run Audit
```
