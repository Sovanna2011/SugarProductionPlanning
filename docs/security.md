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

The mechanism is pluggable, because which one is right depends on what the
factory already runs.

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
| `none` *(default)* | No authentication. Every endpoint is open, no roles are checked, and the actor falls back to whatever the caller declared. The server logs a warning naming this file on every start. |
| `local` | The application's own user table. People sign in with a name and password and receive a server-side session. Needs nothing beyond the database that is already there. |
| `proxy` | Trusts an upstream reverse proxy to have authenticated the user and to pass the identity in a header. |

#### `local`

```bash
SPP_AUTH_MODE=local
SPP_SESSION_TTL=12h                  # how long a session lasts, whatever the activity
SPP_SESSION_COOKIE=spp_session       # the cookie name
SPP_SESSION_COOKIE_SECURE=true       # required wherever the site is served over HTTPS
```

What it does:

- **Passwords are bcrypt digests** at cost 11. The password itself is never
  stored, never logged, and never returned.
- **Sessions are server-side, and only the SHA-256 of the token is stored.** A
  copy of the database yields no usable session. Revoking one — a sign-out, a
  password change, a deactivated account — takes effect on the next request
  rather than at the next expiry.
- **The session cookie is `HttpOnly` and `SameSite=Lax`**, so a cross-site
  scripting bug cannot read it and another site cannot cause the browser to
  send it on a form post. `Secure` is off by default because a plain-HTTP
  evaluation deployment would otherwise have a cookie the browser silently
  discards, which looks exactly like a broken login; **turn it on for any
  deployment reached over HTTPS.**
- **Sign-in failures are indistinguishable.** A wrong password, an unknown
  user and a deactivated account all return the same sentence, so the staff
  list cannot be enumerated from the login screen. An account that cannot sign
  in still costs a bcrypt comparison, so it cannot be identified by timing
  either.
- **Five wrong passwords hold the account for fifteen minutes.** It is a delay,
  not a lock: guessing becomes hopeless, while somebody who mistyped their
  password is not shut out until an administrator rescues them.
- **A password set by somebody else is flagged.** The account may read and may
  change that password. Every other write is refused until it does, because
  until then the audit trail cannot honestly say the action was theirs.
- **Failed sign-ins are throttled per client address**, fifteen in fifteen
  minutes by default, answered `429` with a `Retry-After`. This is the limit
  the per-account hold cannot provide: one guess each against a hundred
  accounts never trips any single account's counter. **Only failures count** —
  a successful sign-in costs nothing and clears the caller's slate, so a shift
  change where forty people sign in at once is unaffected.

`Authorization: Bearer <token>` is accepted as well as the cookie, so the API
can be exercised with `curl` and from test code. The token is in the login
response body for that reason.

#### Who the client is

The rate limiter and the session log both need to know who is calling.
`X-Forwarded-For` is believed **only** when the request actually arrived from a
configured proxy:

```bash
SPP_TRUSTED_PROXIES=10.0.0.7,172.16.0.0/12   # addresses or CIDR blocks
```

Neither extreme works. Trusting the header always would let a caller pick a
fresh identity per request and walk straight past the limiter, and write
anything they liked into the session log. Trusting it never means that behind a
reverse proxy every request appears to come from the proxy — turning a
per-client limit into one global limit that the first attacker closes for
everybody. Only the last hop the trusted chain saw is used; entries to its left
were supplied by the caller and mean nothing.

Leave it unset when the application is reached directly. The server says on
start which of the two it is doing.

#### `proxy`

```bash
SPP_AUTH_MODE=proxy
SPP_AUTH_USER_HEADER=X-Forwarded-User      # the subject; this is what is recorded
SPP_AUTH_NAME_HEADER=X-Forwarded-Name      # optional display name
SPP_AUTH_ROLES_HEADER=X-Forwarded-Groups   # the roles; see the table below
```

**`proxy` mode is only as trustworthy as the network in front of it.** The
header is believed unconditionally, so the application must be reachable *only*
through the proxy. If a caller can connect to the port directly, they can set
the header themselves and the mode buys nothing.

Since roles are now enforced, a proxy deployment **must** pass the roles header
with values from the table below, or its users will be able to read everything
and change nothing.

### Roles

Roles describe jobs on the site rather than screens in the application, so a
new screen inherits an answer to "who may use this" instead of needing a new
role invented for it.

| Role | May |
|---|---|
| `ADMIN` | Everything, including user administration and forcing a blocked posting |
| `PLANNER` | Maintain master data and the daily storage plan |
| `WAREHOUSE` | Post receipts, issues, transfers and reservations |
| `VIEWER` | Read every screen; every write is refused |

The rules are one table in `internal/httpapi/auth.go`, checked in order, rather
than a check scattered across thirty handlers — which is how an endpoint ends
up quietly open.

| Endpoint | Needs |
|---|---|
| `/api/v1/admin/**` | `ADMIN` |
| `POST`/`PUT` `/api/v1/master/**` | `ADMIN` or `PLANNER` |
| `POST /api/v1/planning/**` | `ADMIN` or `PLANNER` |
| `POST /api/v1/inventory/movements/validate` | any signed-in user |
| `POST /api/v1/inventory/**` | `ADMIN` or `WAREHOUSE` |
| everything else | any signed-in user |

Asking whether a receipt *would* fit is a read dressed as a `POST`, which is
why validation stays open — it is how a planner checks a plan.

Reachable without a session, because a browser has to load the application and
ask about it before anyone has signed in: `/api/v1/health`,
`/api/v1/auth/config`, `/api/v1/auth/login`, `/api/v1/auth/me`,
`/api/v1/auth/logout`, and the static files.

Roles match case-insensitively, because identity providers disagree about
casing. A role the system does not enforce is refused at the point of granting
it rather than stored and ignored: a role that grants nothing but looks like it
does is worse than no role at all.

### Accounts

No account is created by a migration. A migration runs on every deployment, so
seeding accounts there would give every installation the same known passwords
and there would be no moment at which somebody decided that was acceptable.

```bash
spp-seed-users -admin sovanna -name "Hang Sovanna"   # one administrator, password printed once
spp-seed-users -demo                                 # the fixture accounts, for evaluation
spp-seed-users -remove-demo                          # deactivate them again
```

### The demo accounts

`spp-seed-users -demo` creates one account per role so the role separation can
be tried out — signed in as each in turn, seeing which buttons appear and which
postings are refused.

| User | Roles | Demonstrates |
|---|---|---|
| `admin` | `ADMIN` | Everything, including user administration |
| `planner` | `PLANNER` | Master data and the plan; cannot post stock |
| `warehouse` | `WAREHOUSE` | Receipts, issues, reservations; cannot change master data |
| `refinery` | `WAREHOUSE`, `PLANNER` | Two roles at once |
| `viewer` | `VIEWER` | Reads everything; every write is refused |

**They all share the password `Demo-Sugar-2027`,** which is in the source, in
this file, and on the login screen of any deployment that has them. One
password across five accounts is exactly what nobody should do with real ones;
it is right here for the same reason it is wrong there — these accounts exist
to be used by whoever has the page open.

That is only acceptable because a database holding them says so. The accounts
carry an `is_demo` flag, the server counts them and warns on **every start**,
the login screen labels them, and the user list tags them. Run
`spp-seed-users -remove-demo` before the system holds anything real; the
accounts are deactivated rather than deleted, so the audit trail still names
them.

### What is still open

**`none` is the default,** so an unconfigured deployment is exactly as open as
it was before. That is deliberate — turning authentication on is a decision
about the factory's environment, not something to impose by default — but it
means the warning at startup is the only thing standing between a fresh
deployment and an open system.

**A request that skips the proxy is anonymous, not rejected.** Under `proxy`
mode a request arriving without the header is logged as a warning and treated
as unauthenticated rather than refused, because in a correct deployment it
cannot happen and refusing would mostly break local debugging. `local` mode
does reject: it has a login screen to send the caller to.

**No OIDC.** If the factory would rather the application validate tokens from
an identity provider directly than trust a proxy, that is a new `Mode` in
`internal/auth` and nothing else changes.

**No password reset by email.** An administrator issues a new password, which
is shown once and must be changed at first sign-in. That suits a site where
the administrator and the user are on the same premises; it does not scale
beyond that.

**Sessions do not slide.** A session lasts `SPP_SESSION_TTL` from the moment it
was issued, whatever the activity, so somebody working a long shift is signed
out mid-shift. Twelve hours is chosen to cover one; a shorter TTL would need
renewal on activity to be usable.

**No password expiry or history.** A password can be changed back to the
previous one, and none of them age out. Both are deliberate: forced rotation
mostly produces `Sugar2027!` becoming `Sugar2028!`, and a history check needs
old digests kept around.

### Authorising capacity overrides

Section 24.5 permits exceeding physical capacity only "unless an authorized
business rule explicitly permits an override". `SPP_OVERRIDE_ROLE` names the
role a user must hold to force a blocked posting.

```bash
SPP_AUTH_MODE=local
SPP_OVERRIDE_ROLE=ADMIN
```

| Situation | Result |
|---|---|
| No role configured *(default)* | Anyone may override, with a reason. The original behaviour. |
| Role configured, request anonymous | `403` — including when authentication is off, so a half-configured deployment fails closed |
| Role configured, user lacks it | `403`, naming who was refused |
| Role configured, user holds it | Posts, with the override flag, reason and the authenticated actor recorded |

### CORS

`Access-Control-Allow-Origin: *` with no `Allow-Credentials`. A browser
refuses to send the session cookie cross-origin against a wildcard, so another
site cannot read a signed-in user's data — and under `local` mode an
uncredentialed cross-origin request is answered `401` anyway.

The cost is that the UI5 app's `?api=http://host:port` override does not work
against a `local`-mode server: the cookie is same-origin only. Serving the app
from the API process, which is what the Docker image does, is the supported
arrangement.

## Other things to settle before production

- **The compose password defaults to `spp`.** It is overridable with
  `SPP_DB_PASSWORD`, but the default should not survive a real deployment, and
  the database port should not be published outside the compose network.
- **TLS terminates wherever you put it.** The server speaks plain HTTP; put it
  behind a proxy that terminates TLS — and set `SPP_SESSION_COOKIE_SECURE=true`
  when you do, or the session cookie will travel over plain HTTP on any
  request that reaches the server directly.
- **Rate limiting covers sign-in only.** The rest of the API is unthrottled,
  which is reasonable for an internal system on a trusted network and worth
  revisiting if it is ever exposed more widely.
- **The sign-in limiter is per process.** Two instances behind a load balancer
  each keep their own count, so the effective limit is the configured one times
  the number of instances. For a shared count it would have to live in the
  database or in something like Redis; at these rates the difference does not
  change the conclusion for an attacker.
- **Backups.** The movement ledger is the system of record for stock; the
  planning workbook is not a substitute for backing it up.
