#!/usr/bin/env bash
#
# Makes this Codespace's demo reachable by anybody with the link, and prints
# the link.
#
#   ./scripts/share.sh          # public
#   ./scripts/share.sh private  # back to just you
#
# What this actually does: a forwarded port in a Codespace is private by
# default, so only the person who opened it can reach it. Public means exactly
# what it says — no GitHub account, no permission check, anybody with the URL.
#
# The demo accounts have a password published in this repository. So a public
# forward is a system anybody who finds the link can sign in to and change. On
# invented seed data for an afternoon of user testing that is the point. On
# anything real it is a mistake, and no amount of "it is only a demo" makes it
# otherwise — remove the demo accounts first (spp-seed-users -remove-demo).

set -euo pipefail

PORT=8080
WANT="${1:-public}"

if [ -z "${CODESPACE_NAME:-}" ]; then
  cat >&2 <<'MSG'
This only works inside a GitHub Codespace — it is the Codespace's port
forwarding that produces a public URL.

Running the demo somewhere else? Then it is reachable at whatever address that
machine has. See README.md.
MSG
  exit 1
fi

case "$WANT" in
  public|private) ;;
  *) echo "usage: $0 [public|private]" >&2; exit 2 ;;
esac

echo "Setting port $PORT to $WANT..."
gh codespace ports visibility "$PORT:$WANT" --codespace "$CODESPACE_NAME"

URL="https://${CODESPACE_NAME}-${PORT}.${GITHUB_CODESPACES_PORT_FORWARDING_DOMAIN:-app.github.dev}"

echo
if [ "$WANT" = "public" ]; then
  cat <<MSG
======================================================================
  The demo is now public. Anybody with this link can sign in:

      $URL

  Accounts: admin / planner / warehouse / refinery / viewer
  Password: Demo-Sugar-2027

  It stays up while this Codespace runs, and stops when it idles out.
  To take it back down:  ./scripts/share.sh private
======================================================================
MSG
else
  cat <<MSG
Port $PORT is private again. Only you can reach $URL
MSG
fi
