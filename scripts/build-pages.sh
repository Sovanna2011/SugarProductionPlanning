#!/usr/bin/env bash
#
# Turns the demo pages into a static site for GitHub Pages.
#
#   ./scripts/build-pages.sh [output-dir]     # defaults to build/pages
#
# Two things have to change. The pages are also published as standalone
# artifacts, where they link to each other by absolute URL; side by side on
# Pages they should link to the neighbouring file. And each page is written as
# a fragment — the artifact publisher wraps it in a document — while a web
# server hands the file over exactly as it is, so the document has to be there.

set -euo pipefail

cd "$(dirname "$0")/.."

OUT="${1:-build/pages}"
rm -rf "$OUT"
mkdir -p "$OUT"
cp demo/*.html "$OUT/"

# --- link the pages to each other -------------------------------------------

sed -i \
  -e 's|https://claude.ai/code/artifact/af8ca334-a6e4-4836-885d-afd507e614c4|login.html|g' \
  -e 's|https://claude.ai/code/artifact/ace6edcf-3d65-4546-8d0f-8816bd1682ef|storage-capacity-console.html|g' \
  -e 's|https://claude.ai/code/artifact/1102d3c9-4851-4002-bc1a-de948174722f|login-and-roles.html|g' \
  "$OUT/index.html"

# --- make each fragment a document ------------------------------------------

python3 - "$OUT" <<'PY'
import glob
import os
import sys

out = sys.argv[1]

HEAD = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
"""

# The artifact host supplies a favicon; a web server does not, so every page
# would ask for /favicon.ico and take a 404. The same emoji each page carries
# as an artifact, inline, so the tab is recognisable and nothing 404s.
FAVICONS = {
    "index.html": "\U0001F3ED",
    "login.html": "\U0001F511",
    "login-and-roles.html": "\U0001F510",
    "storage-capacity-console.html": "\U0001F4CA",
}


def favicon_link(name):
    emoji = FAVICONS.get(name, "\U0001F3ED")
    svg = ("<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'>"
           "<text y='26' font-size='26'>" + emoji + "</text></svg>")
    from urllib.parse import quote
    return '<link rel="icon" href="data:image/svg+xml,' + quote(svg) + '">\n'

for path in sorted(glob.glob(os.path.join(out, "*.html"))):
    src = open(path).read()
    if "<!doctype" in src.lower():
        print(f"  {os.path.basename(path)}: already a document")
        continue

    # Everything up to and including the last stylesheet is head material;
    # what follows is the page. Splitting there rather than on a particular
    # wrapper element keeps this working for pages built at different times,
    # which do not share one.
    marker = "</style>"
    if marker not in src:
        raise SystemExit(f"{path}: no stylesheet, so where the head ends is a guess")
    split = src.rindex(marker) + len(marker)

    open(path, "w").write(HEAD + favicon_link(os.path.basename(path)) + src[:split]
                          + "\n</head>\n<body>\n" + src[split:] + "\n</body>\n</html>\n")
    print(f"  {os.path.basename(path)}: wrapped ({os.path.getsize(path)} bytes)")
PY

# --- refuse to publish something broken --------------------------------------

fail=0
for f in "$OUT"/*.html; do
  name=$(basename "$f")
  grep -qi '<!doctype html>' "$f" || { echo "$name: no doctype"; fail=1; }
  grep -q '</html>'          "$f" || { echo "$name: not closed"; fail=1; }
  grep -q '<body>'           "$f" || { echo "$name: no body"; fail=1; }
  grep -q 'rel="icon"'       "$f" || { echo "$name: no favicon, so it will 404 on one"; fail=1; }
  # A page that still points at an artifact URL would send a visitor away from
  # the site they are already on.
  if [ "$name" = "index.html" ] && grep -q 'claude.ai/code/artifact' "$f"; then
    echo "$name: still links to an artifact URL"
    fail=1
  fi
done
[ "$fail" = 0 ] || exit 1

echo
echo "Built $(ls -1 "$OUT"/*.html | wc -l) pages into $OUT:"
ls -la "$OUT"
