-- 0002_storage_master.sql
-- Storage types, storage locations, pooled storage groups and the
-- warehouse + product + packaging capacity matrix.
--
-- Requirement sections 1-3, 7-13, 21 and 22.

BEGIN;

-- ---------------------------------------------------------------------------
-- Storage types (requirement section 2)
-- ---------------------------------------------------------------------------
-- New storage types are inserted as data, so a fourth kind of silo does not
-- need a schema change.
CREATE TABLE storage_types (
    id                      BIGSERIAL PRIMARY KEY,
    code                    TEXT        NOT NULL UNIQUE,
    name                    TEXT        NOT NULL,
    -- Which unit the *location* capacity is expressed in by default.
    default_capacity_uom_id BIGINT      NOT NULL REFERENCES uoms (id),
    -- Tanks and conditioning silos hold unpacked product: their capacity is
    -- managed by weight/volume only (requirement section 10).
    tracks_packages         BOOLEAN     NOT NULL DEFAULT TRUE,
    description             TEXT        NOT NULL DEFAULT '',
    status                  TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by              TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by              TEXT        NOT NULL DEFAULT 'SYSTEM',
    version                 INTEGER     NOT NULL DEFAULT 1
);

-- ---------------------------------------------------------------------------
-- Storage locations (requirement section 3)
-- ---------------------------------------------------------------------------
CREATE TABLE storage_locations (
    id                          BIGSERIAL PRIMARY KEY,
    factory_id                  BIGINT      NOT NULL REFERENCES factories (id),
    storage_code                TEXT        NOT NULL,
    storage_name                TEXT        NOT NULL,
    storage_type_id             BIGINT      NOT NULL REFERENCES storage_types (id),

    physical_capacity           NUMERIC(18, 3) NOT NULL CHECK (physical_capacity >= 0),
    capacity_uom_id             BIGINT      NOT NULL REFERENCES uoms (id),

    minimum_stock_level         NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (minimum_stock_level >= 0),
    -- The share of physical capacity that may be filled under normal operation.
    safe_capacity_percentage    NUMERIC(6, 3)  NOT NULL DEFAULT 100
        CHECK (safe_capacity_percentage > 0 AND safe_capacity_percentage <= 100),

    allow_mixed_products        BOOLEAN     NOT NULL DEFAULT TRUE,
    allow_mixed_batches         BOOLEAN     NOT NULL DEFAULT TRUE,

    status                      TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    effective_from              DATE        NOT NULL DEFAULT DATE '1900-01-01',
    effective_to                DATE,

    remark                      TEXT        NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version                     INTEGER     NOT NULL DEFAULT 1,

    UNIQUE (factory_id, storage_code),
    CONSTRAINT storage_locations_effective_range
        CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

CREATE INDEX storage_locations_by_type ON storage_locations (factory_id, storage_type_id);

-- ---------------------------------------------------------------------------
-- Storage groups
-- ---------------------------------------------------------------------------
-- The 2026/27 plan tracks raw sugar against the combined 110,000 t of
-- Raw Sugar Warehouse 1 + 2, and finished sugar against the combined 69,000 t
-- of Finished Sugar Warehouse 1 + 3. A group is a named pool of locations that
-- share one planning line and one pooled capacity ceiling.
CREATE TABLE storage_groups (
    id              BIGSERIAL PRIMARY KEY,
    factory_id      BIGINT      NOT NULL REFERENCES factories (id),
    group_code      TEXT        NOT NULL,
    group_name      TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    remark          TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    version         INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (factory_id, group_code)
);

CREATE TABLE storage_group_members (
    storage_group_id    BIGINT NOT NULL REFERENCES storage_groups (id) ON DELETE CASCADE,
    storage_location_id BIGINT NOT NULL REFERENCES storage_locations (id) ON DELETE CASCADE,
    PRIMARY KEY (storage_group_id, storage_location_id)
);

-- ---------------------------------------------------------------------------
-- Storage + product + packaging capacity (requirement sections 4, 12, 22)
-- ---------------------------------------------------------------------------
-- One row per (location, product, packaging). Adding "Warehouse 2 may now hold
-- White Sugar Jumbo" is an INSERT, never a new column.
--
-- These are *product-specific ceilings*, not additive physical capacity
-- (requirement section 13). The engine validates a receipt against this row
-- AND against the location's physical capacity.
CREATE TABLE storage_product_capacity (
    id                          BIGSERIAL PRIMARY KEY,
    storage_location_id         BIGINT      NOT NULL REFERENCES storage_locations (id),
    product_id                  BIGINT      NOT NULL REFERENCES products (id),
    packaging_type_id           BIGINT      NOT NULL REFERENCES packaging_types (id),

    -- Either limit may be left NULL, meaning "not constrained on this axis".
    maximum_package_quantity    NUMERIC(18, 3) CHECK (maximum_package_quantity IS NULL OR maximum_package_quantity >= 0),
    maximum_weight_quantity     NUMERIC(18, 3) CHECK (maximum_weight_quantity IS NULL OR maximum_weight_quantity >= 0),
    weight_uom_id               BIGINT      NOT NULL REFERENCES uoms (id),

    minimum_stock_quantity      NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (minimum_stock_quantity >= 0),
    -- Optional absolute safe ceiling; when NULL the location's
    -- safe_capacity_percentage is applied to the maximum instead.
    maximum_safe_quantity       NUMERIC(18, 3) CHECK (maximum_safe_quantity IS NULL OR maximum_safe_quantity >= 0),

    effective_from              DATE        NOT NULL DEFAULT DATE '1900-01-01',
    effective_to                DATE,

    status                      TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    remark                      TEXT        NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version                     INTEGER     NOT NULL DEFAULT 1,

    CONSTRAINT storage_product_capacity_effective_range
        CHECK (effective_to IS NULL OR effective_to >= effective_from),
    CONSTRAINT storage_product_capacity_at_least_one_limit
        CHECK (maximum_package_quantity IS NOT NULL OR maximum_weight_quantity IS NOT NULL)
);

-- Only one capacity row may be in force for a given combination on a given day.
CREATE INDEX storage_product_capacity_lookup
    ON storage_product_capacity (storage_location_id, product_id, packaging_type_id, effective_from DESC);

-- ---------------------------------------------------------------------------
-- Capacity alert thresholds (requirement section 18)
-- ---------------------------------------------------------------------------
-- Fully configurable bands. `factory_id IS NULL` is the global default set.
CREATE TABLE capacity_threshold_levels (
    id                  BIGSERIAL PRIMARY KEY,
    factory_id          BIGINT      REFERENCES factories (id),
    code                TEXT        NOT NULL,
    name                TEXT        NOT NULL,
    -- Half-open band [from_percentage, to_percentage). The last band uses NULL
    -- for to_percentage, meaning "and above".
    from_percentage     NUMERIC(6, 3) NOT NULL CHECK (from_percentage >= 0),
    to_percentage       NUMERIC(6, 3),
    severity            TEXT        NOT NULL CHECK (severity IN ('NORMAL', 'WARNING', 'HIGH', 'CRITICAL', 'FULL')),
    -- Drives the UI5 semantic colour (None/Good/Critical/Error).
    ui_state            TEXT        NOT NULL DEFAULT 'None',
    sort_order          INTEGER     NOT NULL DEFAULT 0,
    status              TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    version             INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (factory_id, code),
    CONSTRAINT capacity_threshold_band
        CHECK (to_percentage IS NULL OR to_percentage > from_percentage)
);

COMMIT;
