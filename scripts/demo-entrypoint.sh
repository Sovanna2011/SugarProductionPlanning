#!/bin/sh
#
# Brings the demo system up in one step: schema, the plan's own stock position,
# the accounts, then the server.
#
# Every step is safe to repeat, because a container restarts. Migrations are
# already idempotent, seeding stock is a no-op once stock is there, and the
# accounts are left alone if they exist. So `docker compose restart` does not
# duplicate anything and does not fail.
#
# This is the demo path only. A real deployment runs spp-server directly and
# seeds deliberately — see docs/security.md.

set -eu

echo "==> Applying migrations"
spp-migrate

echo
echo "==> Loading the plan's stock position for ${SPP_DEMO_DATE:-2027-02-15}"
spp-seed-demo -if-empty -date "${SPP_DEMO_DATE:-2027-02-15}"

echo
echo "==> Creating the demo accounts"
spp-seed-users -demo

echo
echo "======================================================================"
echo "  Sugar Production Planning — demo system"
echo
echo "  Open:     http://localhost:${SPP_DEMO_PORT:-8080}"
echo "  Sign in:  admin / planner / warehouse / refinery / viewer"
echo "  Password: Demo-Sugar-2027   (also listed on the login screen)"
echo
echo "  These accounts have a published password. This database must not"
echo "  hold anything real. Remove them with:"
echo "      docker compose -f docker-compose.demo.yml exec app spp-seed-users -remove-demo"
echo "======================================================================"
echo

exec spp-server
