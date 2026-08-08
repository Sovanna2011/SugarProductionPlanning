#!/usr/bin/env python3
"""Read the seed migrations and write the demo system's data bundle as JSON.

The browser demo (demo/system.html) has to show the same master data, the same
capacities and the same season plan as the real system. Those numbers already
exist in backend/migrations, generated from the workbook. Re-typing them into a
web page would guarantee they drift apart, and a demo that quietly disagrees
with the system is worse than no demo, so they are read straight out of the
migrations instead.

    python3 scripts/extract_seed_data.py build/seed-data.json

The parser handles only the two shapes those files use:

    INSERT INTO t (cols) VALUES (...), (...);
    INSERT INTO t (cols) SELECT ... FROM (VALUES (...), (...)) AS v(names) ...

which is enough, and small enough to read.
"""

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MIGRATIONS = os.path.join(ROOT, "backend", "migrations")

MASTER = os.path.join(MIGRATIONS, "0005_seed_master_data.sql")
SEASON = os.path.join(MIGRATIONS, "0006_seed_season_2026_2027.sql")


# --- reading SQL -------------------------------------------------------------

def statements(text):
    """Split SQL on semicolons that are outside strings and comments.

    Both matter here. Several remarks contain a semicolon ("Jumbo bagging out
    of the covered pool; Net drawdown ..."), and the season file's header
    comment quotes a sheet name with an apostrophe in it — reading that as the
    start of a string puts the remainder of the file inside one.
    """
    out, cur, quoted = [], "", False
    i = 0
    while i < len(text):
        ch = text[i]
        if not quoted and text[i:i + 2] == "--":
            nl = text.find("\n", i)
            i = len(text) if nl < 0 else nl
            continue
        if quoted:
            if ch == "'":
                if text[i:i + 2] == "''":
                    cur += "''"
                    i += 2
                    continue
                quoted = False
        elif ch == "'":
            quoted = True
        elif ch == ";":
            out.append(cur)
            cur = ""
            i += 1
            continue
        cur += ch
        i += 1
    out.append(cur)
    return out


def tuples(body):
    """Every top-level (...) group in a VALUES list, as raw text."""
    out, cur, depth, quoted = [], "", 0, False
    i = 0
    while i < len(body):
        ch = body[i]
        if quoted:
            cur += ch
            if ch == "'":
                if body[i:i + 2] == "''":
                    cur += "'"
                    i += 2
                    continue
                quoted = False
            i += 1
            continue
        if ch == "'":
            quoted = True
            cur += ch
        elif ch == "(":
            depth += 1
            if depth == 1:
                cur = ""
            else:
                cur += ch
        elif ch == ")":
            depth -= 1
            if depth == 0:
                out.append(cur)
                cur = ""
            else:
                cur += ch
        elif depth > 0:
            cur += ch
        i += 1
    return out


NUMBER = re.compile(r"-?\d+(?:\.\d+)?$")


def value(raw):
    raw = raw.strip()
    if raw.startswith("DATE ") or raw.startswith("TIMESTAMP "):
        return raw.split("'")[1]
    if raw.startswith("'"):
        return raw[1:-1].replace("''", "'")
    raw = raw.split("::")[0].strip()   # drop the explicit cast
    if raw in ("TRUE", "FALSE"):
        return raw == "TRUE"
    if raw == "NULL":
        return None
    if NUMBER.match(raw):
        f = float(raw)
        return int(f) if f == int(f) else f
    return raw


def fields(tup):
    """Split one tuple's text on top-level commas."""
    out, cur, depth, quoted = [], "", 0, False
    i = 0
    while i < len(tup):
        ch = tup[i]
        if quoted:
            cur += ch
            if ch == "'":
                if tup[i:i + 2] == "''":
                    cur += "'"
                    i += 2
                    continue
                quoted = False
            i += 1
            continue
        if ch == "'":
            quoted = True
            cur += ch
        elif ch in "([":
            depth += 1
            cur += ch
        elif ch in ")]":
            depth -= 1
            cur += ch
        elif ch == "," and depth == 0:
            out.append(cur)
            cur = ""
        else:
            cur += ch
        i += 1
    out.append(cur)
    return [value(p) for p in out]


def read(path):
    """Return {table: [dict per row]} plus, for SELECT-form blocks, the tail
    text of the statement, which is where the scope of a season block is
    stated."""
    blocks = []
    for stmt in statements(open(path).read()):
        m = re.search(r"INSERT INTO (\w+)\s*\((.*?)\)\s*SELECT(.*?)FROM \(VALUES(.*)\) AS v\(([^)]*)\)(.*)",
                      stmt, re.S)
        if m:
            table, _, _, body, names, tail = m.groups()
            cols = [c.strip() for c in names.split(",")]
        else:
            m = re.search(r"INSERT INTO (\w+)\s*\((.*?)\)\s*VALUES(.*)", stmt, re.S)
            if not m:
                continue
            table, names, body = m.groups()
            tail = ""
            cols = [c.strip() for c in names.split(",")]
        rows = []
        for tup in tuples(body):
            v = fields(tup)
            if len(v) != len(cols):
                sys.exit(f"{path}: {table} row has {len(v)} values for {len(cols)} columns:\n  {tup}")
            rows.append(dict(zip(cols, v)))
        blocks.append((table, rows, tail))
    return blocks


# --- assembling the bundle ---------------------------------------------------

def main():
    out_path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(ROOT, "build", "seed-data.json")

    data = {}
    for table, rows, _ in read(MASTER):
        data.setdefault(table, []).extend(rows)

    bundle = {
        "uoms": data["uoms"],
        "storageTypes": data["storage_types"],
        "products": data["products"],
        "packaging": data["packaging_types"],
        "productPackaging": data["product_packaging"],
        "locations": data["storage_locations"],
        "groups": data["storage_groups"],
        "groupMembers": data["storage_group_members"],
        "productCapacity": data["storage_product_capacity"],
        "movementTypes": data["movement_types"],
        "bands": data["capacity_threshold_levels"],
        "production": [],
        "storagePlan": {},
    }

    for table, rows, tail in read(SEASON):
        if table == "seasons":
            bundle["season"] = rows[0]
        elif table == "daily_production_plans":
            for r in rows:
                bundle["production"].append([
                    r["plan_date"], r["day_no"], r["cane_target"], r["raw_sugar_target"],
                    r["raw_to_remelt_target"], r["raw_silo_to_remelt_target"],
                    r["raw_packing_target"], r["refined_sugar_target"],
                    r["white_sugar_target"], r["super_refined_target"],
                    r["quota_sales_target"], 1 if r["is_cleaning_day"] else 0, r["remark"],
                ])
        elif table == "daily_storage_plans":
            scope = re.search(r"(?:group_code|storage_code) = '([A-Z0-9-]+)'", tail)
            if not scope:
                sys.exit("a storage plan block does not say which scope it is for")
            bundle["storagePlan"][scope.group(1)] = [
                [r["plan_date"], r["opening"], r["stock_in"], r["stock_out"], r["closing"]]
                for r in rows
            ]

    # Dates have to come out as plain yyyy-mm-dd. When they did not, every
    # lookup by date quietly returned nothing and the demo opened with three
    # tanks of molasses and nine empty warehouses — wrong, and wrong in a way
    # that looks like a design decision rather than a bug.
    iso = re.compile(r"^\d{4}-\d{2}-\d{2}$")
    dated = [bundle["production"][0][0]] + [s[0][0] for s in bundle["storagePlan"].values()]
    for d in dated:
        if not iso.match(str(d)):
            sys.exit(f"a plan date came out as {d!r}, not yyyy-mm-dd")

    # The demo opens on the plan's own position for this day, so every scope
    # has to have one.
    opening_day = "2027-02-15"
    for scope, series in bundle["storagePlan"].items():
        if not any(r[0] == opening_day for r in series):
            sys.exit(f"{scope} has no planned position for {opening_day}, which the demo opens with")

    # --- refuse to emit a bundle that does not reconcile ---------------------
    # These are the workbook's own headline figures. If a change to the
    # migrations moves them, the extraction is wrong or the plan changed, and
    # either way nobody should find out from a demo page.
    checks = [
        ("cane", sum(r[2] for r in bundle["production"]), 2_300_000),
        ("raw sugar", sum(r[3] for r in bundle["production"]), 253_000),
        ("finished sugar", sum(r[7] + r[8] + r[9] for r in bundle["production"]), 242_100),
        ("storage locations", len(bundle["locations"]), 9),
        ("capacity rows", len(bundle["productCapacity"]), 16),
        ("plan days", len(bundle["production"]), 276),
    ]
    bad = [f"{name}: got {got:,g}, expected {want:,g}" for name, got, want in checks if got != want]
    if bad:
        sys.exit("the extracted data does not reconcile:\n  " + "\n  ".join(bad))

    os.makedirs(os.path.dirname(os.path.abspath(out_path)), exist_ok=True)
    with open(out_path, "w") as f:
        json.dump(bundle, f, separators=(",", ":"), sort_keys=False)

    print(f"{out_path}: {os.path.getsize(out_path):,} bytes")
    for name, got, _ in checks:
        print(f"  {name}: {got:,g}")


if __name__ == "__main__":
    main()
