-- 0004_planning.sql
-- Crushing seasons, the daily production plan and the daily storage plan.
--
-- Requirement sections 15-17 and 19. The daily storage plan is what makes
-- future capacity projection possible: today's balance rolled forward through
-- the approved plan.

BEGIN;

CREATE TABLE seasons (
    id              BIGSERIAL PRIMARY KEY,
    factory_id      BIGINT      NOT NULL REFERENCES factories (id),
    code            TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    crushing_start  DATE        NOT NULL,
    crushing_end    DATE        NOT NULL,
    -- Remelt/refinery processing continues after crushing stops.
    season_end      DATE        NOT NULL,
    cane_target     NUMERIC(18, 3) NOT NULL DEFAULT 0,
    recovery_rate   NUMERIC(6, 3)  NOT NULL DEFAULT 0,
    status          TEXT        NOT NULL DEFAULT 'PLANNED'
        CHECK (status IN ('PLANNED', 'ACTIVE', 'CLOSED')),
    remark          TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by      TEXT        NOT NULL DEFAULT 'SYSTEM',
    version         INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (factory_id, code),
    CONSTRAINT seasons_date_order CHECK (crushing_end >= crushing_start AND season_end >= crushing_end)
);

-- ---------------------------------------------------------------------------
-- Daily production plan
-- ---------------------------------------------------------------------------
-- One row per calendar day of the season, mirroring the planning workbook.
CREATE TABLE daily_production_plans (
    id                          BIGSERIAL PRIMARY KEY,
    season_id                   BIGINT      NOT NULL REFERENCES seasons (id),
    factory_id                  BIGINT      NOT NULL REFERENCES factories (id),
    plan_date                   DATE        NOT NULL,
    day_no                      INTEGER,

    cane_target                 NUMERIC(18, 3) NOT NULL DEFAULT 0,
    cane_actual                 NUMERIC(18, 3) NOT NULL DEFAULT 0,

    raw_sugar_target            NUMERIC(18, 3) NOT NULL DEFAULT 0,
    raw_sugar_actual            NUMERIC(18, 3) NOT NULL DEFAULT 0,
    -- Freshly produced raw sugar fed straight to the refinery without ever
    -- being stored (crushing season only).
    raw_to_remelt_target        NUMERIC(18, 3) NOT NULL DEFAULT 0,
    raw_to_remelt_actual        NUMERIC(18, 3) NOT NULL DEFAULT 0,
    -- Raw sugar drawn back out of the warehouses and fed to the refinery.
    -- This is the series that draws the raw pool down after crushing ends.
    raw_silo_to_remelt_target   NUMERIC(18, 3) NOT NULL DEFAULT 0,
    raw_silo_to_remelt_actual   NUMERIC(18, 3) NOT NULL DEFAULT 0,
    -- Raw sugar packed into jumbo bags to relieve warehouse pressure.
    raw_packing_target          NUMERIC(18, 3) NOT NULL DEFAULT 0,
    raw_packing_actual          NUMERIC(18, 3) NOT NULL DEFAULT 0,

    refined_sugar_target        NUMERIC(18, 3) NOT NULL DEFAULT 0,
    refined_sugar_actual        NUMERIC(18, 3) NOT NULL DEFAULT 0,
    white_sugar_target          NUMERIC(18, 3) NOT NULL DEFAULT 0,
    white_sugar_actual          NUMERIC(18, 3) NOT NULL DEFAULT 0,
    super_refined_target        NUMERIC(18, 3) NOT NULL DEFAULT 0,
    super_refined_actual        NUMERIC(18, 3) NOT NULL DEFAULT 0,

    -- Contracted quota deliveries out of the finished goods warehouses.
    quota_sales_target          NUMERIC(18, 3) NOT NULL DEFAULT 0,
    quota_sales_actual          NUMERIC(18, 3) NOT NULL DEFAULT 0,

    is_cleaning_day             BOOLEAN     NOT NULL DEFAULT FALSE,
    remark                      TEXT        NOT NULL DEFAULT '',
    status                      TEXT        NOT NULL DEFAULT 'APPROVED'
        CHECK (status IN ('DRAFT', 'APPROVED', 'CLOSED')),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version                     INTEGER     NOT NULL DEFAULT 1,
    UNIQUE (season_id, plan_date)
);

CREATE INDEX daily_production_plans_by_date
    ON daily_production_plans (factory_id, plan_date);

-- ---------------------------------------------------------------------------
-- Daily storage plan (requirement section 15)
-- ---------------------------------------------------------------------------
-- A plan line targets either a single storage location or a pooled storage
-- group, never both. Product and packaging are optional: the 2026/27 workbook
-- plans raw sugar at pool level without splitting by packaging, while a
-- finished goods line is usually product + packaging specific.
CREATE TABLE daily_storage_plans (
    id                          BIGSERIAL PRIMARY KEY,
    season_id                   BIGINT      REFERENCES seasons (id),
    factory_id                  BIGINT      NOT NULL REFERENCES factories (id),
    plan_date                   DATE        NOT NULL,

    storage_location_id         BIGINT      REFERENCES storage_locations (id),
    storage_group_id            BIGINT      REFERENCES storage_groups (id),
    product_id                  BIGINT      REFERENCES products (id),
    packaging_type_id           BIGINT      REFERENCES packaging_types (id),

    opening_weight              NUMERIC(18, 3) NOT NULL DEFAULT 0,
    planned_in_weight           NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (planned_in_weight >= 0),
    planned_out_weight          NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (planned_out_weight >= 0),
    planned_closing_weight      NUMERIC(18, 3) NOT NULL DEFAULT 0,

    opening_packages            NUMERIC(18, 3) NOT NULL DEFAULT 0,
    planned_in_packages         NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (planned_in_packages >= 0),
    planned_out_packages        NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (planned_out_packages >= 0),
    planned_closing_packages    NUMERIC(18, 3) NOT NULL DEFAULT 0,

    weight_uom_id               BIGINT      NOT NULL REFERENCES uoms (id),
    remark                      TEXT        NOT NULL DEFAULT '',
    status                      TEXT        NOT NULL DEFAULT 'APPROVED'
        CHECK (status IN ('DRAFT', 'APPROVED', 'CLOSED')),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version                     INTEGER     NOT NULL DEFAULT 1,

    CONSTRAINT daily_storage_plans_one_scope
        CHECK ((storage_location_id IS NULL) <> (storage_group_id IS NULL))
);

-- A plan line is unique per day and scope; COALESCE keeps the optional
-- product/packaging axes out of the way of a NULL-tolerant unique index.
CREATE UNIQUE INDEX daily_storage_plans_key ON daily_storage_plans (
    plan_date,
    COALESCE(storage_location_id, 0),
    COALESCE(storage_group_id, 0),
    COALESCE(product_id, 0),
    COALESCE(packaging_type_id, 0)
);

CREATE INDEX daily_storage_plans_by_date ON daily_storage_plans (factory_id, plan_date);

COMMIT;
