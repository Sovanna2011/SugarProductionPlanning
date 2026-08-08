#!/usr/bin/env bash
#
# The demo system, from source, for a machine with Go and PostgreSQL but no
# Docker.
#
#   ./scripts/demo.sh
#   open http://localhost:8080
#
# It builds the UI5 app if it is not already built, applies migrations, loads
# the plan's own stock position, creates the five accounts, and serves. Every
# step is safe to repeat.
#
# Options:
#   --port 8080              listen address
#   --date 2027-04-10        which day of the season to take the position from
#   --database-url <dsn>     an existing PostgreSQL to use
#   --reset                  drop and recreate the database first
#   --no-build               skip the UI5 build (API only)
#
# For Docker instead:  docker compose -f docker-compose.demo.yml up --build

set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$PWD"

PORT=8080
DATE=2027-02-15
DSN="${SPP_DATABASE_URL:-postgres://spp:spp@127.0.0.1:5432/spp_demo?sslmode=disable}"
RESET=0
BUILD=1

while [ $# -gt 0 ]; do
  case "$1" in
    --port)         PORT="$2"; shift 2 ;;
    --date)         DATE="$2"; shift 2 ;;
    --database-url) DSN="$2";  shift 2 ;;
    --reset)        RESET=1;   shift ;;
    --no-build)     BUILD=0;   shift ;;
    -h|--help)      sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

say() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }

# --- prerequisites ----------------------------------------------------------

command -v go >/dev/null || { echo "Go is not installed. See https://go.dev/dl/" >&2; exit 1; }
command -v psql >/dev/null || echo "note: psql not found; skipping the database checks below"

if [ "$RESET" = 1 ]; then
  DB_NAME="${DSN##*/}"; DB_NAME="${DB_NAME%%\?*}"
  say "Dropping and recreating $DB_NAME"
  dropdb --if-exists "$DB_NAME" 2>/dev/null || true
  createdb "$DB_NAME"
fi

export SPP_DATABASE_URL="$DSN"

# --- the dashboard ----------------------------------------------------------

WEB_DIR=""
if [ "$BUILD" = 1 ]; then
  if [ -f "$ROOT/dist/resources/sap-ui-core.js" ]; then
    say "Using the dashboard already built in dist/"
  else
    command -v npm >/dev/null || {
      echo "npm is not installed, so the dashboard cannot be built." >&2
      echo "Re-run with --no-build to start the API alone." >&2
      exit 1
    }
    say "Building the dashboard (a couple of minutes the first time)"
    [ -d "$ROOT/node_modules" ] || npm ci --no-audit --no-fund
    npm run build
  fi
  WEB_DIR="$ROOT/dist"
fi

# --- the database -----------------------------------------------------------

cd "$ROOT/backend"

say "Applying migrations"
go run ./cmd/migrate

say "Loading the plan's stock position for $DATE"
go run ./cmd/seed-demo -if-empty -date "$DATE"

say "Creating the demo accounts"
go run ./cmd/seed-users -demo

# --- serve ------------------------------------------------------------------

cat <<BANNER

======================================================================
  Sugar Production Planning — demo system

  Open:     http://localhost:$PORT
  Sign in:  admin / planner / warehouse / refinery / viewer
  Password: Demo-Sugar-2027   (also listed on the login screen)

  Each account has different roles, so the screens and the buttons
  change with it. What to try: docs/demo-user-test.md

  These accounts have a published password. This database must not
  hold anything real:  go run ./cmd/seed-users -remove-demo
======================================================================

BANNER

exec env \
  SPP_ADDR=":$PORT" \
  SPP_WEB_DIR="$WEB_DIR" \
  SPP_AUTH_MODE=local \
  SPP_OVERRIDE_ROLE=ADMIN \
  go run ./cmd/server
