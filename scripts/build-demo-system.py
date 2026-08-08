#!/usr/bin/env python3
"""Assemble demo/system.html from its template, the fonts and the seed data.

    python3 scripts/build-demo-system.py

The result is one self-contained file with no external requests at all: it
works from a file:// URL, from GitHub Pages and from the artifact host, whose
content policy forbids fetching anything from another origin.

Two things get inlined:

  {{FONTS}}  the five IBM Plex faces in demo/assets/fonts, as data URIs
  {{DATA}}   the master data and season plan, read out of the migrations by
             scripts/extract_seed_data.py

Both are inlined at build time rather than fetched at run time, and both come
from files in this repository rather than from anything typed into the page.
"""

import base64
import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TEMPLATE = os.path.join(ROOT, "demo", "src", "system.template.html")
FONT_DIR = os.path.join(ROOT, "demo", "assets", "fonts")
OUT = os.path.join(ROOT, "demo", "system.html")

# family, weight, file, unicode-range
#
# The Khmer and Thai faces carry a unicode-range, so a browser only downloads
# — here, only decodes — them for text that actually needs them. Latin text
# keeps falling through to Plex whatever language the interface is in, which is
# what keeps a code like FG-WH01 looking the same in all three.
KHMER = "U+1780-17FF,U+19E0-19FF,U+200B"
THAI = "U+0E01-0E5B,U+200B"

FACES = [
    ("IBM Plex Sans", 400, "ibm-plex-sans-latin-400-normal.woff2", None),
    ("IBM Plex Sans", 500, "ibm-plex-sans-latin-500-normal.woff2", None),
    ("IBM Plex Sans", 600, "ibm-plex-sans-latin-600-normal.woff2", None),
    ("IBM Plex Mono", 500, "ibm-plex-mono-latin-500-normal.woff2", None),
    ("IBM Plex Serif", 600, "ibm-plex-serif-latin-600-normal.woff2", None),
    ("Noto Sans Khmer", 400, "noto-sans-khmer-khmer-400-normal.woff2", KHMER),
    ("Noto Sans Khmer", 600, "noto-sans-khmer-khmer-600-normal.woff2", KHMER),
    ("Noto Sans Thai", 400, "noto-sans-thai-thai-400-normal.woff2", THAI),
    ("Noto Sans Thai", 600, "noto-sans-thai-thai-600-normal.woff2", THAI),
]


def font_css():
    out = []
    for family, weight, name, ranges in FACES:
        path = os.path.join(FONT_DIR, name)
        if not os.path.exists(path):
            sys.exit(f"missing font: {path}")
        b64 = base64.b64encode(open(path, "rb").read()).decode("ascii")
        out.append(
            f"@font-face{{font-family:'{family}';font-style:normal;font-weight:{weight};"
            f"font-display:swap;src:url(data:font/woff2;base64,{b64}) format('woff2');"
            + (f"unicode-range:{ranges};" if ranges else "")
            + "}"
        )
    return "\n".join(out)


def main():
    data_path = os.path.join(ROOT, "build", "seed-data.json")
    subprocess.run([sys.executable, os.path.join(ROOT, "scripts", "extract_seed_data.py"), data_path],
                   check=True)
    data = open(data_path).read()
    json.loads(data)   # a malformed bundle should fail here, not in a browser

    page = open(TEMPLATE).read()
    for token in ("/*{{FONTS}}*/", "/*{{DATA}}*/"):
        if token not in page:
            sys.exit(f"the template no longer contains {token}")
    page = page.replace("/*{{FONTS}}*/", font_css()).replace("/*{{DATA}}*/", data)

    # A page that still carries a placeholder, or that lost its script, would
    # publish as a blank screen — which is exactly the failure nobody notices
    # until somebody else opens it.
    for must in ("{{", "</script>", "id=\"login\"", "id=\"app\""):
        if must == "{{":
            if must in page:
                sys.exit("a placeholder survived the build")
        elif must not in page:
            sys.exit(f"the built page has no {must}")

    open(OUT, "w").write(page)
    print(f"{OUT}: {os.path.getsize(OUT):,} bytes")


if __name__ == "__main__":
    main()
