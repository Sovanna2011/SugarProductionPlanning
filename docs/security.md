# Security posture

An honest account of where this system stands. Written after a review of the
API and deployment surface, so that whoever deploys it knows what they are
deploying rather than discovering it later.

## What is in place

**Parameterised SQL throughout.** Every query passes values as parameters. The
one place a value was concatenated (the `LIMIT` on the movement ledger) was
bounded to 1–1000 and is now a parameter too, so no query needs reasoning about
where its values came from.

**Internal errors are not returned to callers.** A 4xx carries its message
verbatim — a capacity rule firing or a stale version is exactly what the caller
needs to read. A 5xx does not: database errors name tables, columns and
constraints, so those are logged in full server-side and answered with a
generic message.

**Request bodies are bounded** at 4 MB and reject unknown fields, so a
misspelled key is reported rather than silently ignored.

**Writes are transactional.** A movement and its balance update commit
together, so stock can never move without a ledger line, or the reverse.
Reservations lock the balance row, so two concurrent reservations cannot take
the same free stock.

**Optimistic locking on master data.** A stale `version` is refused with 409
rather than silently overwriting a concurrent edit.

**Capacity overrides are recorded.** Forcing a blocked posting requires a
reason, and both the flag and the reason are stored on the movement. A database
constraint enforces that the reason cannot be empty.

**The container runs as a non-root user**, holds no secrets, and is built from
a static binary with no shell dependencies beyond busybox.

## Authentication

The plumbing is in place and the mechanism is pluggable, because which one is
right depends on what the factory already runs.

Once a request is authenticated, **the audit trail takes the actor from the
authenticated identity and ignores the `postedBy` / `updatedBy` field in the
request body.** A caller claiming to be somebody else is recorded as
themselves. That is the property that matters: section 24 permits exceeding
physical capacity only under an authorised override, so the record of who
authorised it has to mean something.

### Modes

`SPP_AUTH_MODE` selects the mechanism.

| Mode | Behaviour |
|---|---|
| `none` *(default)* | No authentication. Every endpoint is open and the actor falls back to whatever the caller declared. The server logs a warning naming this file on every start. |
| `proxy` | Trusts an upstream reverse proxy to have authenticated the user and to pass the identity in a header. |

```bash
SPP_AUTH_MODE=proxy
SPP_AUTH_USER_HEADER=X-Forwarded-User      # the subject; this is what is recorded
SPP_AUTH_NAME_HEADER=X-Forwarded-Name      # optional display name
SPP_AUTH_ROLES_HEADER=X-Forwarded-Groups   # optional, comma separated
```

**`proxy` mode is only as trustworthy as the network in front of it.** The
header is believed unconditionally, so the application must be reachable *only*
through the proxy. If a caller can connect to the port directly, they can set
the header themselves and the mode buys nothing.

### What is still open

**`none` is the default,** so an unconfigured deployment is exactly as open as
it was before. That is deliberate — turning authentication on is a decision
about the factory's environment, not something to impose by default — but it
means the warning at startup is the only thing standing between a fresh
deployment and an open system.

**Only the capacity override is authorised.** Roles are carried into the
request context and `SPP_OVERRIDE_ROLE` gates the one operation the
requirement says needs authorising. No other endpoint checks roles, so any
authenticated user can still read anything and change master data.

**A request that skips the proxy is anonymous, not rejected.** Under `proxy`
mode a request arriving without the header is logged as a warning and treated
as unauthenticated rather than refused, because in a correct deployment it
cannot happen and refusing would mostly break local debugging. Once a mechanism
is settled, this should become a 401.

**No OIDC.** If the factory would rather the application validate tokens from
an identity provider directly than trust a proxy, that is a new `Mode` in
`internal/auth` and nothing else changes.

### Authorising capacity overrides

Section 24.5 permits exceeding physical capacity only "unless an authorized
business rule explicitly permits an override". `SPP_OVERRIDE_ROLE` names the
role a user must hold to force a blocked posting.

```bash
SPP_AUTH_MODE=proxy
SPP_OVERRIDE_ROLE=warehouse-supervisor
```

| Situation | Result |
|---|---|
| No role configured *(default)* | Anyone may override, with a reason. The original behaviour. |
| Role configured, request anonymous | `403` — including when authentication is off, so a half-configured deployment fails closed |
| Role configured, user lacks it | `403`, naming who was refused |
| Role configured, user holds it | Posts, with the override flag, reason and the authenticated actor recorded |

Roles match case-insensitively, because identity providers disagree about
casing.

**CORS is `Access-Control-Allow-Origin: *`,** which is harmless while there are
no credentials to steal and should be narrowed once there are.

## Other things to settle before production

- **The compose password defaults to `spp`.** It is overridable with
  `SPP_DB_PASSWORD`, but the default should not survive a real deployment, and
  the database port should not be published outside the compose network.
- **TLS terminates wherever you put it.** The server speaks plain HTTP; put it
  behind a proxy that terminates TLS.
- **No rate limiting.** Reasonable for an internal system on a trusted network,
  worth revisiting if it is ever exposed more widely.
- **Backups.** The movement ledger is the system of record for stock; the
  planning workbook is not a substitute for backing it up.
