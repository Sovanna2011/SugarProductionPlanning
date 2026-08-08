-- 0001_core_master_data.sql
-- Organisation, unit-of-measure, product and packaging masters.
--
-- Everything that a business user is expected to change over time lives in a
-- table, never in Go code (requirement section 5: "Do not hard-code package
-- sizes in Golang").

BEGIN;

CREATE TABLE schema_migrations (
    version     TEXT PRIMARY KEY,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Units of measure
-- ---------------------------------------------------------------------------
-- `dimension` separates weight from package counts so the capacity engine can
-- refuse to compare bags against tons. `factor_to_base` converts a quantity to
-- the base unit of its dimension (TON for WEIGHT, PC for COUNT).
CREATE TABLE uoms (
    id              BIGSERIAL PRIMARY KEY,
    code            TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL,
    dimension       TEXT        NOT NULL CHECK (dimension IN ('WEIGHT', 'COUNT', 'VOLUME')),
    factor_to_base  NUMERIC(18, 9) NOT NULL CHECK (factor_to_base > 0),
    is_base         BOOLEAN     NOT NULL DEFAULT FALSE,
    status          TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    version         INTEGER     NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX uoms_one_base_per_dimension
    ON uoms (dimension) WHERE is_base;

-- ---------------------------------------------------------------------------
-- Company / factory
-- ---------------------------------------------------------------------------
CREATE TABLE companies (
    id          BIGSERIAL PRIMARY KEY,
    code        TEXT        NOT NULL UNIQUE,
    name        TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version     INTEGER     NOT NULL DEFAULT 1
);

CREATE TABLE factories (
    id          BIGSERIAL PRIMARY KEY,
    company_id  BIGINT      NOT NULL REFERENCES companies (id),
    code        TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    timezone    TEXT        NOT NULL DEFAULT 'Asia/Phnom_Penh',
    status      TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version     INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (company_id, code)
);

-- ---------------------------------------------------------------------------
-- Products
-- ---------------------------------------------------------------------------
CREATE TABLE products (
    id              BIGSERIAL PRIMARY KEY,
    code            TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL,
    product_group   TEXT        NOT NULL CHECK (product_group IN ('MOLASSES', 'RAW_SUGAR', 'FINISHED_SUGAR', 'BY_PRODUCT')),
    -- Stock keeping unit for the product itself; packaging adds the second axis.
    base_uom_id     BIGINT      NOT NULL REFERENCES uoms (id),
    is_bulk_liquid  BOOLEAN     NOT NULL DEFAULT FALSE,
    status          TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    remark          TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    version         INTEGER     NOT NULL DEFAULT 1
);

-- ---------------------------------------------------------------------------
-- Packaging master (requirement section 5)
-- ---------------------------------------------------------------------------
CREATE TABLE packaging_types (
    id                  BIGSERIAL PRIMARY KEY,
    code                TEXT        NOT NULL UNIQUE,
    description         TEXT        NOT NULL,
    -- Net content of one package, expressed in `net_weight_uom_id`.
    net_weight          NUMERIC(18, 6) NOT NULL CHECK (net_weight >= 0),
    net_weight_uom_id   BIGINT      NOT NULL REFERENCES uoms (id),
    -- Denormalised convenience copy in tons; the single source used by the
    -- package <-> weight conversion (requirement section 14).
    weight_in_ton       NUMERIC(18, 6) NOT NULL CHECK (weight_in_ton >= 0),
    -- Bulk "packaging" carries no discrete package count (silo, tank, bulk pile).
    is_bulk             BOOLEAN     NOT NULL DEFAULT FALSE,
    status              TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    version             INTEGER     NOT NULL DEFAULT 1,
    -- A non-bulk package must weigh something, otherwise bags <-> tons breaks.
    CONSTRAINT packaging_types_weight_required
        CHECK (is_bulk OR weight_in_ton > 0)
);

-- ---------------------------------------------------------------------------
-- Product + packaging combination (requirement section 6)
-- ---------------------------------------------------------------------------
CREATE TABLE product_packaging (
    id                  BIGSERIAL PRIMARY KEY,
    product_id          BIGINT      NOT NULL REFERENCES products (id),
    packaging_type_id   BIGINT      NOT NULL REFERENCES packaging_types (id),
    sku_code            TEXT        NOT NULL UNIQUE,
    sku_name            TEXT        NOT NULL,
    is_default          BOOLEAN     NOT NULL DEFAULT FALSE,
    status              TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    version             INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (product_id, packaging_type_id)
);

CREATE UNIQUE INDEX product_packaging_one_default_per_product
    ON product_packaging (product_id) WHERE is_default;

COMMIT;
