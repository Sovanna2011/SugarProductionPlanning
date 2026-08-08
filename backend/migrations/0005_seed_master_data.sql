-- 0005_seed_master_data.sql
-- Reference and master data for Kampong Speu Sugar Co., Ltd.
--
-- Capacity figures marked "PLAN" come from the corrected daily production plan
-- workbook (ProductionPlan_2627_2.3mt Rev.1) and its summary report. Figures
-- marked "EXAMPLE" are the illustrative numbers from the requirement document
-- and must be confirmed with operations before go-live; every one of them is
-- editable master data.

BEGIN;

-- ---------------------------------------------------------------------------
-- Units of measure
-- ---------------------------------------------------------------------------
INSERT INTO uoms (code, name, dimension, factor_to_base, is_base) VALUES
    ('TON', 'Metric Ton',  'WEIGHT', 1,        TRUE),
    ('KG',  'Kilogram',    'WEIGHT', 0.001,    FALSE),
    ('PC',  'Piece',       'COUNT',  1,        TRUE),
    ('BAG', 'Bag',         'COUNT',  1,        FALSE),
    ('M3',  'Cubic Metre', 'VOLUME', 1,        TRUE);

-- ---------------------------------------------------------------------------
-- Company / factory
-- ---------------------------------------------------------------------------
INSERT INTO companies (code, name) VALUES
    ('KSS', 'Kampong Speu Sugar Co., Ltd.');

INSERT INTO factories (company_id, code, name)
SELECT id, 'KSS-F1', 'Kampong Speu Sugar Factory 1' FROM companies WHERE code = 'KSS';

-- ---------------------------------------------------------------------------
-- Storage types (requirement section 2)
-- ---------------------------------------------------------------------------
INSERT INTO storage_types (code, name, default_capacity_uom_id, tracks_packages, description)
SELECT v.code, v.name, u.id, v.tracks_packages, v.description
FROM (VALUES
    ('TANK',                     'Tank',                     FALSE, 'Molasses storage tank; capacity managed by weight'),
    ('RAW_SUGAR_WAREHOUSE',      'Raw Sugar Warehouse',      TRUE,  'Bulk raw sugar pile and jumbo bag storage'),
    ('CONDITIONING_SILO',        'Conditioning Silo',        FALSE, 'Unpacked refined sugar conditioning; capacity managed by weight'),
    ('FINISHED_GOODS_WAREHOUSE', 'Finished Goods Warehouse', TRUE,  'Packed refined and white sugar')
) AS v(code, name, tracks_packages, description)
CROSS JOIN uoms u WHERE u.code = 'TON';

-- ---------------------------------------------------------------------------
-- Products
-- ---------------------------------------------------------------------------
INSERT INTO products (code, name, product_group, base_uom_id, is_bulk_liquid, remark)
SELECT v.code, v.name, v.product_group, u.id, v.is_bulk_liquid, v.remark
FROM (VALUES
    ('MOLASSES',      'Molasses',            'MOLASSES',       TRUE,  'By-product; stored in tanks and sold'),
    ('RAW-SUGAR',     'Raw Sugar',           'RAW_SUGAR',      FALSE, 'Crushing season output; stored, sold or remelted'),
    ('REFINED-SUGAR', 'Refined Sugar',       'FINISHED_SUGAR', FALSE, 'Conditioned then packed'),
    ('WHITE-SUGAR',   'White Sugar',         'FINISHED_SUGAR', FALSE, 'Packed directly'),
    ('SUPER-REFINED', 'Super Refined Sugar', 'FINISHED_SUGAR', FALSE, 'Produced during the remelt period')
) AS v(code, name, product_group, is_bulk_liquid, remark)
CROSS JOIN uoms u WHERE u.code = 'TON';

-- ---------------------------------------------------------------------------
-- Packaging master (requirement section 5)
-- ---------------------------------------------------------------------------
INSERT INTO packaging_types (code, description, net_weight, net_weight_uom_id, weight_in_ton, is_bulk)
SELECT v.code, v.description, v.net_weight, u.id, v.weight_in_ton, v.is_bulk
FROM (VALUES
    ('PKG-BULK',   'Bulk / Unpacked', 0::NUMERIC,    0::NUMERIC,     TRUE),
    ('PKG-25KG',   '25 KG Bag',       25::NUMERIC,   0.025::NUMERIC, FALSE),
    ('PKG-50KG',   '50 KG Bag',       50::NUMERIC,   0.050::NUMERIC, FALSE),
    ('PKG-500KG',  '500 KG Bag',      500::NUMERIC,  0.500::NUMERIC, FALSE),
    ('PKG-1000KG', '1,000 KG Bag',    1000::NUMERIC, 1.000::NUMERIC, FALSE),
    ('PKG-JUMBO',  'Jumbo Bag 1.1 T', 1100::NUMERIC, 1.100::NUMERIC, FALSE)
) AS v(code, description, net_weight, weight_in_ton, is_bulk)
CROSS JOIN uoms u WHERE u.code = 'KG';

-- ---------------------------------------------------------------------------
-- Product + packaging combinations (requirement section 6)
-- ---------------------------------------------------------------------------
INSERT INTO product_packaging (product_id, packaging_type_id, sku_code, sku_name, is_default)
SELECT p.id, k.id, v.sku_code, v.sku_name, v.is_default
FROM (VALUES
    ('MOLASSES',      'PKG-BULK',  'MOL-BULK',   'Bulk Molasses',            TRUE),
    ('RAW-SUGAR',     'PKG-BULK',  'RAW-BULK',   'Bulk Raw Sugar',           TRUE),
    ('RAW-SUGAR',     'PKG-JUMBO', 'RAW-JUMBO',  'Raw Sugar Jumbo Bag',      FALSE),
    ('RAW-SUGAR',     'PKG-50KG',  'RAW-50KG',   'Raw Sugar 50 KG',          FALSE),
    ('REFINED-SUGAR', 'PKG-BULK',  'REF-BULK',   'Bulk Refined Sugar',       FALSE),
    ('REFINED-SUGAR', 'PKG-50KG',  'REF-50KG',   'Refined Sugar 50 KG',      TRUE),
    ('REFINED-SUGAR', 'PKG-JUMBO', 'REF-JUMBO',  'Refined Sugar Jumbo Bag',  FALSE),
    ('WHITE-SUGAR',   'PKG-50KG',  'WHT-50KG',   'White Sugar 50 KG',        TRUE),
    ('WHITE-SUGAR',   'PKG-JUMBO', 'WHT-JUMBO',  'White Sugar Jumbo Bag',    FALSE),
    ('SUPER-REFINED', 'PKG-50KG',  'SREF-50KG',  'Super Refined Sugar 50 KG', TRUE)
) AS v(product_code, packaging_code, sku_code, sku_name, is_default)
JOIN products p         ON p.code = v.product_code
JOIN packaging_types k  ON k.code = v.packaging_code;

-- ---------------------------------------------------------------------------
-- Storage locations (requirement sections 1, 3, 26)
-- ---------------------------------------------------------------------------
INSERT INTO storage_locations (
    factory_id, storage_code, storage_name, storage_type_id,
    physical_capacity, capacity_uom_id, safe_capacity_percentage,
    allow_mixed_products, allow_mixed_batches, status, remark)
SELECT f.id, v.storage_code, v.storage_name, st.id,
       v.physical_capacity, u.id, v.safe_pct,
       v.mixed_products, TRUE, v.status, v.remark
FROM (VALUES
    ('MOL-T01', 'Molasses Tank 1',           'TANK',                     5000::NUMERIC,  95::NUMERIC, FALSE, 'ACTIVE',
     'EXAMPLE capacity (requirement section 7) - confirm actual tank volume'),
    ('MOL-T02', 'Molasses Tank 2',           'TANK',                     5000::NUMERIC,  95::NUMERIC, FALSE, 'ACTIVE',
     'EXAMPLE capacity (requirement section 7) - confirm actual tank volume'),
    ('MOL-T03', 'Molasses Tank 3',           'TANK',                     5000::NUMERIC,  95::NUMERIC, FALSE, 'ACTIVE',
     'EXAMPLE capacity (requirement section 7) - confirm actual tank volume'),
    ('RAW-WH01', 'Raw Sugar Warehouse 1',    'RAW_SUGAR_WAREHOUSE',      45000::NUMERIC, 95::NUMERIC, TRUE,  'ACTIVE',
     'PLAN capacity 45,000 t (summary report section 3)'),
    ('RAW-WH02', 'Raw Sugar Warehouse 2',    'RAW_SUGAR_WAREHOUSE',      65000::NUMERIC, 95::NUMERIC, TRUE,  'ACTIVE',
     'PLAN capacity 65,000 t (summary report section 3); holds the jumbo bag overflow'),
    ('CON-S01', 'Conditioning Silo 1',       'CONDITIONING_SILO',        2000::NUMERIC,  95::NUMERIC, FALSE, 'ACTIVE',
     'EXAMPLE capacity 2,000 t (requirement section 17); plan holds a steady 1,600 t working stock'),
    ('FG-WH01', 'Finished Sugar Warehouse 1', 'FINISHED_GOODS_WAREHOUSE', 22000::NUMERIC, 95::NUMERIC, TRUE, 'ACTIVE',
     'PLAN capacity 22,000 t (summary report section 5, "Refined Sugar Warehouse 1")'),
    ('FG-WH02', 'Finished Sugar Warehouse 2', 'FINISHED_GOODS_WAREHOUSE', 0::NUMERIC,     95::NUMERIC, TRUE, 'INACTIVE',
     'Present in the storage structure but not part of the 2026/27 plan, whose 69,000 t is W1 + W3 only. Set a capacity and activate when it comes into use.'),
    ('FG-WH03', 'Finished Sugar Warehouse 3', 'FINISHED_GOODS_WAREHOUSE', 47000::NUMERIC, 95::NUMERIC, TRUE, 'ACTIVE',
     'PLAN capacity 47,000 t (summary report section 5, "Refined Sugar Warehouse 3")')
) AS v(storage_code, storage_name, type_code, physical_capacity, safe_pct, mixed_products, status, remark)
JOIN storage_types st ON st.code = v.type_code
CROSS JOIN factories f
CROSS JOIN uoms u WHERE f.code = 'KSS-F1' AND u.code = 'TON';

-- ---------------------------------------------------------------------------
-- Pooled storage groups
-- ---------------------------------------------------------------------------
INSERT INTO storage_groups (factory_id, group_code, group_name, remark)
SELECT f.id, v.group_code, v.group_name, v.remark
FROM (VALUES
    ('RAW-POOL', 'Raw Sugar Warehouses (W1 + W2)',      'Planned as one 110,000 t pool in the 2026/27 workbook'),
    ('FG-POOL',  'Finished Sugar Warehouses (W1 + W3)', 'Planned as one 69,000 t pool in the 2026/27 workbook')
) AS v(group_code, group_name, remark)
CROSS JOIN factories f WHERE f.code = 'KSS-F1';

INSERT INTO storage_group_members (storage_group_id, storage_location_id)
SELECT g.id, l.id
FROM (VALUES
    ('RAW-POOL', 'RAW-WH01'),
    ('RAW-POOL', 'RAW-WH02'),
    ('FG-POOL',  'FG-WH01'),
    ('FG-POOL',  'FG-WH03')
) AS v(group_code, storage_code)
JOIN storage_groups g    ON g.group_code = v.group_code
JOIN storage_locations l ON l.storage_code = v.storage_code;

-- ---------------------------------------------------------------------------
-- Storage + product + packaging capacity (requirement sections 4, 8, 12, 22)
-- ---------------------------------------------------------------------------
-- NULL on an axis means "not constrained on this axis"; the location's
-- physical capacity still applies on top of every row (section 13).
INSERT INTO storage_product_capacity (
    storage_location_id, product_id, packaging_type_id,
    maximum_package_quantity, maximum_weight_quantity, weight_uom_id, remark)
SELECT l.id, p.id, k.id, v.max_packages, v.max_weight, u.id, v.remark
FROM (VALUES
    -- Molasses tanks: weight only, no package dimension.
    ('MOL-T01', 'MOLASSES',      'PKG-BULK',  NULL::NUMERIC,     5000::NUMERIC,  'Whole tank available to molasses'),
    ('MOL-T02', 'MOLASSES',      'PKG-BULK',  NULL::NUMERIC,     5000::NUMERIC,  'Whole tank available to molasses'),
    ('MOL-T03', 'MOLASSES',      'PKG-BULK',  NULL::NUMERIC,     5000::NUMERIC,  'Whole tank available to molasses'),

    -- Raw sugar warehouse 1 (45,000 t physical).
    ('RAW-WH01', 'RAW-SUGAR',    'PKG-BULK',  NULL::NUMERIC,     30000::NUMERIC, 'EXAMPLE split from requirement section 8'),
    ('RAW-WH01', 'RAW-SUGAR',    'PKG-JUMBO', 10000::NUMERIC,    11000::NUMERIC, 'EXAMPLE split from requirement section 8: 10,000 bags x 1.1 t'),
    -- Raw sugar warehouse 2 (65,000 t physical) takes the jumbo bag overflow.
    ('RAW-WH02', 'RAW-SUGAR',    'PKG-BULK',  NULL::NUMERIC,     45000::NUMERIC, 'Derived from physical capacity - confirm bulk/jumbo split'),
    ('RAW-WH02', 'RAW-SUGAR',    'PKG-JUMBO', 18182::NUMERIC,    20000::NUMERIC, 'Sized for the 20,700 t jumbo bag programme (21-Jan to 30-Mar-2027)'),

    -- Conditioning silo: unpacked refined sugar, weight only (section 10).
    ('CON-S01', 'REFINED-SUGAR', 'PKG-BULK',  NULL::NUMERIC,     2000::NUMERIC,  'Weight-managed; no bag count'),

    -- Finished sugar warehouse 1 (22,000 t physical).
    ('FG-WH01', 'REFINED-SUGAR', 'PKG-50KG',  200000::NUMERIC,   10000::NUMERIC, 'EXAMPLE from requirement section 12'),
    ('FG-WH01', 'REFINED-SUGAR', 'PKG-JUMBO', 5000::NUMERIC,     5500::NUMERIC,  'EXAMPLE from requirement section 12'),
    ('FG-WH01', 'WHITE-SUGAR',   'PKG-50KG',  100000::NUMERIC,   5000::NUMERIC,  'EXAMPLE from requirement section 12'),

    -- Finished sugar warehouse 3 (47,000 t physical). The requirement document
    -- only lists jumbo lines for W3; the 50 KG lines are derived from physical
    -- capacity so the 69,000 t pool is usable end to end.
    ('FG-WH03', 'REFINED-SUGAR', 'PKG-JUMBO', 6000::NUMERIC,     6600::NUMERIC,  'EXAMPLE from requirement section 12'),
    ('FG-WH03', 'WHITE-SUGAR',   'PKG-JUMBO', 6000::NUMERIC,     6600::NUMERIC,  'EXAMPLE from requirement section 12'),
    ('FG-WH03', 'REFINED-SUGAR', 'PKG-50KG',  400000::NUMERIC,   20000::NUMERIC, 'Derived from physical capacity - confirm product split'),
    ('FG-WH03', 'WHITE-SUGAR',   'PKG-50KG',  540000::NUMERIC,   27000::NUMERIC, 'Derived from physical capacity - confirm product split'),
    ('FG-WH03', 'SUPER-REFINED', 'PKG-50KG',  40000::NUMERIC,    2000::NUMERIC,  'Sized for the 2,000 t remelt-period super refined programme')
) AS v(storage_code, product_code, packaging_code, max_packages, max_weight, remark)
JOIN storage_locations l ON l.storage_code = v.storage_code
JOIN products p          ON p.code = v.product_code
JOIN packaging_types k   ON k.code = v.packaging_code
CROSS JOIN uoms u WHERE u.code = 'TON';

-- ---------------------------------------------------------------------------
-- Movement types (requirement section 24)
-- ---------------------------------------------------------------------------
INSERT INTO movement_types (code, name, direction, requires_capacity_check) VALUES
    ('PRODUCTION_RECEIPT', 'Production Receipt',       'IN',     TRUE),
    ('PACKING_RECEIPT',    'Packing Receipt',          'IN',     TRUE),
    ('TRANSFER_IN',        'Stock Transfer In',        'IN',     TRUE),
    ('TANK_RECEIPT',       'Tank Receipt',             'IN',     TRUE),
    ('WAREHOUSE_RECEIPT',  'Warehouse Receipt',        'IN',     TRUE),
    ('CUSTOMER_RETURN',    'Customer Return',          'IN',     TRUE),
    ('TRANSFER_OUT',       'Stock Transfer Out',       'OUT',    FALSE),
    ('SALES_ISSUE',        'Sales / Quota Delivery',   'OUT',    FALSE),
    ('REMELT_ISSUE',       'Issue to Remelt',          'OUT',    FALSE),
    ('PACKING_ISSUE',      'Release to Packing',       'OUT',    FALSE),
    ('LOSS',               'Process Loss',             'OUT',    FALSE),
    ('ADJUSTMENT',         'Stock Adjustment',         'ADJUST', TRUE);

-- ---------------------------------------------------------------------------
-- Capacity alert thresholds (requirement section 18)
-- ---------------------------------------------------------------------------
-- Global default set (factory_id NULL). A factory-specific set overrides it.
INSERT INTO capacity_threshold_levels (factory_id, code, name, from_percentage, to_percentage, severity, ui_state, sort_order) VALUES
    (NULL, 'NORMAL',   'Normal',   0,   80,   'NORMAL',   'Success', 10),
    (NULL, 'WARNING',  'Warning',  80,  90,   'WARNING',  'Warning', 20),
    (NULL, 'HIGH',     'High',     90,  95,   'HIGH',     'Warning', 30),
    (NULL, 'CRITICAL', 'Critical', 95,  100,  'CRITICAL', 'Error',   40),
    (NULL, 'FULL',     'Full',     100, NULL, 'FULL',     'Error',   50);

COMMIT;
