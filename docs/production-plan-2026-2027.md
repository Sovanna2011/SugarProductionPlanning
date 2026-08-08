# Crushing season 2026/27 — plan and storage findings

Source: `docs/source/ProductionPlan_2627_2.3mt_Rev1_Corrected.xlsx`
(sheet `RW'2627(2.3mt)Rev1(re)`) and
`docs/source/KSS_Sugar_Production_Summary_2026_2027.docx`.

Everything below marked "reproduced" was computed by the system from the seeded
plan, not copied from the summary report.

## Season at a glance

| Item | Value |
|---|---|
| Crushing | 1 Dec 2026 → 16 Apr 2027 (137 days) |
| Remelt processing ends | 2 Sep 2027 |
| Cane crushed | 2,300,000 t |
| Raw sugar produced | 253,000 t (11.00% recovery) |
| Raw sugar direct to refinery | 124,950 t |
| Raw sugar to warehouse (net) | 128,050 t |
| Raw sugar drawn back out for remelt | 128,047.5 t |
| Refined sugar | 106,700 t |
| White sugar | 133,400 t |
| Super refined sugar | 2,000 t |
| Quota sales | 136,000 t at 500 t/day over 272 days |
| Cleaning days | 6 |

## Storage capacity

| Storage | Capacity | Source |
|---|---:|---|
| Raw Sugar Warehouse 1 | 45,000 t | Plan |
| Raw Sugar Warehouse 2 | 65,000 t | Plan |
| **Raw sugar pool** | **110,000 t** | Plan |
| Finished Sugar Warehouse 1 | 22,000 t | Plan |
| Finished Sugar Warehouse 3 | 47,000 t | Plan |
| **Finished sugar pool** | **69,000 t** | Plan |
| Conditioning Silo 1 | 2,000 t | Requirement §17 example — confirm |
| Molasses Tanks 1–3 | 5,000 t each | Requirement §7 example — confirm |

Finished Sugar Warehouse 2 appears in the storage structure but is **not part of
the 2026/27 plan**, whose 69,000 t is Warehouses 1 + 3 only. It is seeded
inactive with zero capacity rather than given an invented figure; set a capacity
and activate it when it comes into use.

## Finding 1 — raw sugar fits, but only just, and only because of jumbo bagging

Planned raw sugar storage of 128,050 t exceeds the 110,000 t of warehouse
capacity by 18,050 t. The plan resolves this by packing raw sugar into 1.1 t
jumbo bags and moving it out of the covered warehouses:

- **20,700 t** bagged, at 300 t/day
- between **21 Jan 2027 and 30 Mar 2027**

*Reproduced:* peak raw sugar stock of **109,720 t on 10 Apr 2027** — 280 t below
the 110,000 t ceiling.

The margin is 0.25%. The jumbo bags must be bought before January; if the
programme slips or recovery runs above 11.00%, the warehouses overflow. At a 95%
safe level (104,500 t) the pool is already over its safe ceiling from
**2 Apr 2027**.

## Finding 2 — finished sugar storage is not sufficient

Even with quota sales running at 500 t/day for the whole season, production
outruns the 69,000 t of finished sugar capacity.

*Reproduced:*

| | Date |
|---|---|
| Exceeds the 95% safe level (65,550 t) | **19 May 2027** |
| Exceeds the 69,000 t physical pool | **28 May 2027** |
| Closing stock on 2 Sep 2027 | **106,150 t** — about 37,000 t with nowhere to go |

From late February the plan runs at a steady 900 t/day in (400 refined + 500
white) against 500 t/day out, so the pool gains 400 t every day. Sales at about
**640 t/day** would hold the ending stock within capacity; about **890 t/day**
would clear the whole season's production by 2 Sep 2027.

The summary report also notes that with no refined or white deliveries at all,
the finished warehouses fill on 20 Feb 2027.

## Finding 3 — a 50 t inconsistency in the workbook

On **30 Aug 2027** the workbook's hard-keyed finished goods total column reads
450 t, while its own components sum to 400 t (refined 400 + white 0 + super
refined 0). Every other day of the season reconciles.

The 50 t propagates into the workbook's own tracker, which is why the daily
stock tracker ends at 106,150 t while the summary report states 106,100 t, and
why the tracker totals 242,150 t of production against the summary's 242,100 t.

The seed uses the component columns, so the system reconciles to the summary
report's figures. Worth correcting in the workbook.

## How the plan maps onto the system

The workbook's two right-hand stock trackers map directly onto
`daily_storage_plans`:

| Workbook | System |
|---|---|
| `BX BY BZ CA` — raw sugar W1+W2 | `RAW-POOL` plan lines (137 days) |
| `CD CE CF CG` — refined & white W1+W3 | `FG-POOL` plan lines (276 days) |
| `AA` — conditioning silo stock | `CON-S01` plan lines (272 days) |

Two normalisations were needed:

1. **"Out" columns are keyed negative** in the workbook; the schema stores both
   directions as positive magnitudes.
2. **The raw sugar receipt column is a net flow** (production less what the
   refinery takes). On a cleaning day nothing is produced but remelt keeps
   drawing from store, so it goes negative. It is split back into gross in and
   out movements, which is what a ledger and a plan both need.

`scripts/generate_season_seed.py` performs the extraction and refuses to write a
seed that no longer reconciles — it asserts every total in the table at the top
of this document and checks that each day's opening + in − out equals its
closing.

## Recommended follow-up

1. **Confirm the example capacities.** Molasses tank volumes, the conditioning
   silo capacity and the product/packaging splits are illustrative figures from
   the requirement document. They are ordinary master data and can be corrected
   in place.
2. **Decide Finished Sugar Warehouse 2.** Bringing it into service is the most
   direct answer to Finding 2.
3. **Set the sales target against capacity, not habit.** 500 t/day is known to
   be insufficient; the system can now show the required rate for any assumed
   capacity.
4. **Fix the 50 t total** in the workbook.
5. **Watch the raw sugar margin.** 280 t of headroom on 110,000 t leaves no room
   for a recovery rate above 11.00%. The projection endpoint highlights the
   first breach date as the season progresses.
