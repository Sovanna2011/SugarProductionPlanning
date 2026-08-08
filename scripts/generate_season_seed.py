#!/usr/bin/env python3
"""Generate the 2026/27 season seed migration from the planning workbook.

Reads ``docs/source/ProductionPlan_2627_2.3mt_Rev1_Corrected.xlsx`` and writes
``backend/migrations/0006_seed_season_2026_2027.sql``.

The workbook sheet ``RW'2627(2.3mt)Rev1(re)`` holds one row per calendar day of
the season plus two right-hand stock trackers:

    BX BY BZ CA  raw sugar pool  (Raw WH1 + WH2, ceiling 110,000 t)
    CD CE CF CG  finished pool   (Finished WH1 + WH3, ceiling 69,000 t)

each laid out as opening / in / out / closing, with the "out" figures keyed as
negatives. Those four columns map exactly onto ``daily_storage_plans``.

Run with:  python3 scripts/generate_season_seed.py
"""

from __future__ import annotations

import datetime as dt
import pathlib
import sys

try:
    import openpyxl
except ImportError:  # pragma: no cover - developer convenience
    sys.exit("openpyxl is required: pip install openpyxl")

ROOT = pathlib.Path(__file__).resolve().parent.parent
WORKBOOK = ROOT / "docs" / "source" / "ProductionPlan_2627_2.3mt_Rev1_Corrected.xlsx"
OUTPUT = ROOT / "backend" / "migrations" / "0006_seed_season_2026_2027.sql"
SHEET = "RW'2627(2.3mt)Rev1(re)"

FIRST_ROW, LAST_ROW = 19, 294  # 2026-12-01 .. 2027-09-02

# 1-based worksheet column indexes.
C_DAY_NO = 2
C_DATE = 3
C_CANE_TARGET = 4
C_SUPER_REFINED = 22
C_REFINED_PACKED = 25
C_SILO_STOCK = 27
C_WHITE_PACKED = 42
C_RAW_TO_REMELT_INPUT = 65
C_RAW_PRODUCED = 66
C_RAW_TO_REMELT = 67
C_REMARK = 73
C_RAW_OPENING, C_RAW_IN, C_RAW_OUT, C_RAW_CLOSING = 76, 77, 78, 79
C_FG_OPENING, C_FG_IN, C_FG_OUT, C_FG_CLOSING = 82, 83, 84, 85


def num(value) -> float:
    """Coerce a cell to a number, treating blanks and text as zero."""
    return float(value) if isinstance(value, (int, float)) else 0.0


def q(text: str) -> str:
    """Quote a string for SQL."""
    return "'" + str(text).replace("'", "''") + "'"


def fmt(value: float) -> str:
    return f"{value:.3f}"


class Day:
    __slots__ = (
        "date", "day_no", "cane", "raw_produced", "raw_to_remelt",
        "raw_silo_to_remelt", "raw_packing",
        "refined", "white", "super_refined", "quota_sales", "remark",
        "raw_open", "raw_net", "raw_in", "raw_out", "raw_close", "has_raw_plan",
        "fg_open", "fg_in", "fg_out", "fg_close",
        "silo_open", "silo_in", "silo_out", "silo_close",
    )


def load_days(ws) -> list[Day]:
    days: list[Day] = []
    prev_silo = 0.0
    for row in range(FIRST_ROW, LAST_ROW + 1):
        raw_date = ws.cell(row, C_DATE).value
        if not isinstance(raw_date, dt.datetime):
            continue

        d = Day()
        d.date = raw_date.date()
        day_no = ws.cell(row, C_DAY_NO).value
        d.day_no = int(day_no) if isinstance(day_no, (int, float)) else None

        d.cane = num(ws.cell(row, C_CANE_TARGET).value)
        d.raw_produced = num(ws.cell(row, C_RAW_PRODUCED).value)
        # Two distinct series: fresh raw sugar sent straight to the refinery
        # during crushing, and raw sugar drawn back out of the warehouses
        # (which is what empties the pool once crushing has stopped).
        d.raw_to_remelt = num(ws.cell(row, C_RAW_TO_REMELT).value)
        d.raw_silo_to_remelt = num(ws.cell(row, C_RAW_TO_REMELT_INPUT).value)
        d.refined = num(ws.cell(row, C_REFINED_PACKED).value)
        d.white = num(ws.cell(row, C_WHITE_PACKED).value)
        d.super_refined = num(ws.cell(row, C_SUPER_REFINED).value)
        d.remark = (ws.cell(row, C_REMARK).value or "").strip()

        # Stock trackers. "Out" columns are keyed negative in the workbook;
        # the schema stores both directions as positive magnitudes.
        d.raw_open = num(ws.cell(row, C_RAW_OPENING).value)
        d.raw_close = num(ws.cell(row, C_RAW_CLOSING).value)
        d.has_raw_plan = ws.cell(row, C_RAW_CLOSING).value is not None
        # Raw sugar leaving the covered pool is the jumbo bagging programme.
        d.raw_packing = abs(num(ws.cell(row, C_RAW_OUT).value))
        # The workbook's receipt column is a *net* flow (production less what
        # the refinery takes). On a cleaning day nothing is produced but remelt
        # keeps drawing from store, so it goes negative. Split it back into
        # gross movements, which is what the ledger and the plan both need.
        raw_net = num(ws.cell(row, C_RAW_IN).value)
        d.raw_net = raw_net
        d.raw_in = max(raw_net, 0.0)
        d.raw_out = d.raw_packing + max(-raw_net, 0.0)

        d.fg_open = num(ws.cell(row, C_FG_OPENING).value)
        d.fg_in = num(ws.cell(row, C_FG_IN).value)
        d.fg_out = abs(num(ws.cell(row, C_FG_OUT).value))
        d.fg_close = num(ws.cell(row, C_FG_CLOSING).value)
        d.quota_sales = d.fg_out

        # Conditioning silo: the workbook keys the held stock and the packed
        # output, so the production receipt is the balancing figure.
        d.silo_close = num(ws.cell(row, C_SILO_STOCK).value)
        d.silo_open = prev_silo
        d.silo_out = d.refined
        d.silo_in = d.silo_close - d.silo_open + d.silo_out
        prev_silo = d.silo_close

        days.append(d)
    return days


def check(days: list[Day]) -> None:
    """Fail loudly if the extraction stops reconciling to the summary report."""
    expected = {
        "cane": (sum(d.cane for d in days), 2_300_000),
        "raw produced": (sum(d.raw_produced for d in days), 253_000),
        "raw direct to remelt": (sum(d.raw_to_remelt for d in days), 124_950),
        "raw warehouse to remelt": (sum(d.raw_silo_to_remelt for d in days), 128_047.5),
        "raw net to warehouse": (sum(d.raw_net for d in days), 128_050),
        "raw jumbo packing": (sum(d.raw_packing for d in days), 20_700),
        "refined": (sum(d.refined for d in days), 106_700),
        "white": (sum(d.white for d in days), 133_400),
        "super refined": (sum(d.super_refined for d in days), 2_000),
        "quota sales": (sum(d.quota_sales for d in days), 136_000),
        "peak raw pool": (max(d.raw_close for d in days), 109_720),
    }
    problems = [
        f"  {name}: extracted {got:,.1f} vs expected {want:,.1f}"
        for name, (got, want) in expected.items()
        if abs(got - want) > 0.5
    ]
    if problems:
        sys.exit("Workbook extraction no longer reconciles:\n" + "\n".join(problems))

    for d in days:
        if d.has_raw_plan and abs(d.raw_open + d.raw_in - d.raw_out - d.raw_close) > 0.001:
            sys.exit(f"Raw pool does not balance on {d.date}")
        if abs(d.fg_open + d.fg_in - d.fg_out - d.fg_close) > 0.001:
            sys.exit(f"Finished pool does not balance on {d.date}")
        if d.silo_in < -0.001 or d.silo_out < -0.001:
            sys.exit(f"Conditioning silo movement went negative on {d.date}")


HEADER = """-- 0006_seed_season_2026_2027.sql
-- GENERATED FILE - do not edit by hand.
-- Regenerate with: python3 scripts/generate_season_seed.py
--
-- Source: docs/source/ProductionPlan_2627_2.3mt_Rev1_Corrected.xlsx,
-- sheet "RW'2627(2.3mt)Rev1(re)".
--
-- Note on the finished goods total: the workbook's hard-keyed total column
-- reads 450 t on 2027-08-30 while its components (refined 400 + white 0 +
-- super refined 0) sum to 400 t. This seed uses the components, which is what
-- reconciles to the summary report's 242,100 t of production and 106,100 t of
-- closing stock.

BEGIN;

INSERT INTO seasons (factory_id, code, name, crushing_start, crushing_end, season_end,
                     cane_target, recovery_rate, status, remark)
SELECT id, '2026-2027', 'Crushing Season 2026/27', DATE '2026-12-01', DATE '2027-04-16',
       DATE '2027-09-02', 2300000, 11.000, 'PLANNED',
       'Rev.1 corrected plan: 2.3M t cane at 11.00% recovery'
FROM factories WHERE code = 'KSS-F1';
"""

PRODUCTION_BLOCK = """
-- Daily production plan: one row per calendar day of the season.
INSERT INTO daily_production_plans (
    season_id, factory_id, plan_date, day_no, cane_target,
    raw_sugar_target, raw_to_remelt_target, raw_silo_to_remelt_target,
    raw_packing_target, refined_sugar_target, white_sugar_target,
    super_refined_target, quota_sales_target, is_cleaning_day, remark)
SELECT s.id, s.factory_id, v.plan_date, v.day_no, v.cane_target,
       v.raw_sugar_target, v.raw_to_remelt_target, v.raw_silo_to_remelt_target,
       v.raw_packing_target, v.refined_sugar_target, v.white_sugar_target,
       v.super_refined_target, v.quota_sales_target, v.is_cleaning_day, v.remark
FROM (VALUES
{values}
) AS v(plan_date, day_no, cane_target, raw_sugar_target, raw_to_remelt_target,
       raw_silo_to_remelt_target, raw_packing_target, refined_sugar_target,
       white_sugar_target, super_refined_target, quota_sales_target,
       is_cleaning_day, remark)
CROSS JOIN seasons s WHERE s.code = '2026-2027';
"""

STORAGE_BLOCK = """
-- {comment}
INSERT INTO daily_storage_plans (
    season_id, factory_id, plan_date, {scope_columns},
    opening_weight, planned_in_weight, planned_out_weight,
    planned_closing_weight, weight_uom_id, remark)
SELECT s.id, s.factory_id, v.plan_date, {scope_values},
       v.opening, v.stock_in, v.stock_out, v.closing, u.id, v.remark
FROM (VALUES
{values}
) AS v(plan_date, opening, stock_in, stock_out, closing, remark)
{joins}
CROSS JOIN seasons s
CROSS JOIN uoms u
WHERE s.code = '2026-2027' AND u.code = 'TON'{extra_where};
"""


def storage_block(comment, scope_columns, scope_values, joins, extra_where, rows):
    return STORAGE_BLOCK.format(
        comment=comment,
        scope_columns=scope_columns,
        scope_values=scope_values,
        joins="\n".join(joins),
        extra_where=extra_where,
        values=",\n".join(rows),
    )


def stock_row(date, opening, stock_in, stock_out, closing, remark=""):
    return (
        f"    (DATE '{date}', {fmt(opening)}::NUMERIC, {fmt(stock_in)}::NUMERIC, "
        f"{fmt(stock_out)}::NUMERIC, {fmt(closing)}::NUMERIC, {q(remark)})"
    )


def main() -> None:
    wb = openpyxl.load_workbook(WORKBOOK, data_only=True)
    days = load_days(wb[SHEET])
    if not days:
        sys.exit("no dated rows found in the workbook")
    check(days)

    out = [HEADER]

    production_rows = []
    for d in days:
        cleaning = "TRUE" if "cleaning" in d.remark.lower() else "FALSE"
        production_rows.append(
            f"    (DATE '{d.date}', {d.day_no if d.day_no is not None else 'NULL'}::INTEGER, "
            f"{fmt(d.cane)}::NUMERIC, {fmt(d.raw_produced)}::NUMERIC, "
            f"{fmt(d.raw_to_remelt)}::NUMERIC, {fmt(d.raw_silo_to_remelt)}::NUMERIC, "
            f"{fmt(d.raw_packing)}::NUMERIC, "
            f"{fmt(d.refined)}::NUMERIC, {fmt(d.white)}::NUMERIC, "
            f"{fmt(d.super_refined)}::NUMERIC, {fmt(d.quota_sales)}::NUMERIC, "
            f"{cleaning}, {q(d.remark)})"
        )
    out.append(PRODUCTION_BLOCK.format(values=",\n".join(production_rows)))

    raw_rows = [
        stock_row(d.date, d.raw_open, d.raw_in, d.raw_out, d.raw_close,
                  "; ".join(filter(None, [
                      "Jumbo bagging out of the covered pool" if d.raw_packing else "",
                      "Net drawdown to remelt on a non-crushing day" if d.raw_net < 0 else "",
                  ])))
        for d in days if d.has_raw_plan
    ]
    out.append(storage_block(
        comment="Raw sugar pool (Raw WH1 + WH2, 110,000 t): crushing season days only.",
        scope_columns="storage_group_id, product_id",
        scope_values="scope.id, p.id",
        joins=[
            "JOIN storage_groups scope ON scope.group_code = 'RAW-POOL'",
            "CROSS JOIN products p",
        ],
        extra_where=" AND p.code = 'RAW-SUGAR'",
        rows=raw_rows,
    ))

    fg_rows = [stock_row(d.date, d.fg_open, d.fg_in, d.fg_out, d.fg_close) for d in days]
    out.append(storage_block(
        comment=("Finished sugar pool (Finished WH1 + WH3, 69,000 t): refined + white +\n"
                 "-- super refined combined, as the workbook plans them."),
        scope_columns="storage_group_id",
        scope_values="scope.id",
        joins=["JOIN storage_groups scope ON scope.group_code = 'FG-POOL'"],
        extra_where="",
        rows=fg_rows,
    ))

    silo_rows = [
        stock_row(d.date, d.silo_open, d.silo_in, d.silo_out, d.silo_close)
        for d in days if d.silo_close or d.silo_open
    ]
    out.append(storage_block(
        comment=("Conditioning Silo 1: bulk refined sugar. The workbook keys held stock\n"
                 "-- and packed output, so the production receipt is the balancing figure."),
        scope_columns="storage_location_id, product_id, packaging_type_id",
        scope_values="scope.id, p.id, k.id",
        joins=[
            "JOIN storage_locations scope ON scope.storage_code = 'CON-S01'",
            "CROSS JOIN products p",
            "CROSS JOIN packaging_types k",
        ],
        extra_where=" AND p.code = 'REFINED-SUGAR' AND k.code = 'PKG-BULK'",
        rows=silo_rows,
    ))

    out.append("\nCOMMIT;\n")
    OUTPUT.write_text("\n".join(out))

    print(f"wrote {OUTPUT.relative_to(ROOT)}")
    print(f"  {len(days)} days  {days[0].date} .. {days[-1].date}")
    print(f"  raw pool plan lines:          {len(raw_rows)}")
    print(f"  finished pool plan lines:     {len(fg_rows)}")
    print(f"  conditioning silo plan lines: {len(silo_rows)}")


if __name__ == "__main__":
    main()
