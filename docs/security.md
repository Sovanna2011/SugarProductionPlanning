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

## What is not in place

These two are the same problem seen from two angles, and both need a decision
about how this system authenticates before they can be closed.

### 1. There is no authentication

Every endpoint is open. Anyone who can reach the port can read the whole
inventory, post movements, override capacity blocks and rewrite master data.

The system is currently only safe on a trusted network — behind a reverse proxy
that authenticates, or on a segment reachable solely by the factory's own
machines. `Access-Control-Allow-Origin` is `*`, which is harmless while there
are no credentials to steal and unacceptable once there are.

### 2. The audit trail is self-declared

`postedBy` and `updatedBy` arrive in the request body. The server records
whatever string it is given. So the movement ledger shows who a caller *said*
they were, not who they were.

This matters more here than it would in most systems, because the override
mechanism is the accountability control. Section 24 of the requirement permits
exceeding physical capacity only when "an authorized business rule explicitly
permits an override" — and the record of who authorised it is, at present,
whatever the client typed.

**The fix is the same in both cases:** authenticate the request, then take the
actor from the authenticated identity and ignore the field in the body. That is
a small change to `service` and `httpapi` once the mechanism is chosen. The
mechanism is the open question, because it depends on what the factory already
runs.

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
