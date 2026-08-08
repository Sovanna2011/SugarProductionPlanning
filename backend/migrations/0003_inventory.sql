-- 0003_inventory.sql
-- Inventory movements (the ledger) and inventory balances (the running total).
--
-- Requirement sections 14, 16, 23 and 24. Actual closing stock is never keyed
-- in: it is derived from posted movements (section 16).

BEGIN;

CREATE TABLE movement_types (
    id                  BIGSERIAL PRIMARY KEY,
    code                TEXT        NOT NULL UNIQUE,
    name                TEXT        NOT NULL,
    -- IN increases stock, OUT decreases it, ADJUST may do either (signed).
    direction           TEXT        NOT NULL CHECK (direction IN ('IN', 'OUT', 'ADJUST')),
    -- Receipts are the movements that must pass capacity validation before
    -- posting (requirement section 24).
    requires_capacity_check BOOLEAN NOT NULL DEFAULT FALSE,
    status              TEXT        NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'INACTIVE')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    version             INTEGER     NOT NULL DEFAULT 1
);

-- ---------------------------------------------------------------------------
-- Inventory movements
-- ---------------------------------------------------------------------------
CREATE TABLE inventory_movements (
    id                  BIGSERIAL PRIMARY KEY,
    factory_id          BIGINT      NOT NULL REFERENCES factories (id),
    movement_date       DATE        NOT NULL,
    movement_type_id    BIGINT      NOT NULL REFERENCES movement_types (id),

    storage_location_id BIGINT      NOT NULL REFERENCES storage_locations (id),
    product_id          BIGINT      NOT NULL REFERENCES products (id),
    packaging_type_id   BIGINT      NOT NULL REFERENCES packaging_types (id),
    batch_no            TEXT        NOT NULL DEFAULT '',

    -- Signed quantities: positive for receipts, negative for issues. Both axes
    -- are always stored (requirement section 14); for bulk packaging the
    -- package quantity is 0.
    package_quantity    NUMERIC(18, 3) NOT NULL DEFAULT 0,
    weight_quantity     NUMERIC(18, 3) NOT NULL,
    weight_uom_id       BIGINT      NOT NULL REFERENCES uoms (id),

    -- Set when a transfer moves stock between two locations; the counter-leg
    -- is a second row referencing the same transfer_ref.
    transfer_ref        TEXT        NOT NULL DEFAULT '',
    reference_doc       TEXT        NOT NULL DEFAULT '',
    remark              TEXT        NOT NULL DEFAULT '',

    -- A movement only affects balances once posted.
    posted              BOOLEAN     NOT NULL DEFAULT FALSE,
    posted_at           TIMESTAMPTZ,
    posted_by           TEXT,
    -- Records that an over-capacity posting was consciously overridden
    -- (requirement section 24.5).
    capacity_override   BOOLEAN     NOT NULL DEFAULT FALSE,
    capacity_override_reason TEXT   NOT NULL DEFAULT '',

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by          TEXT        NOT NULL DEFAULT 'SYSTEM',
    version             INTEGER     NOT NULL DEFAULT 1,

    CONSTRAINT inventory_movements_posted_fields
        CHECK ((posted AND posted_at IS NOT NULL) OR (NOT posted AND posted_at IS NULL)),
    CONSTRAINT inventory_movements_override_needs_reason
        CHECK (NOT capacity_override OR capacity_override_reason <> '')
);

CREATE INDEX inventory_movements_by_date
    ON inventory_movements (factory_id, movement_date);
CREATE INDEX inventory_movements_by_balance_key
    ON inventory_movements (storage_location_id, product_id, packaging_type_id, batch_no, movement_date);

-- ---------------------------------------------------------------------------
-- Inventory balances (requirement section 23)
-- ---------------------------------------------------------------------------
-- Keyed by Factory + Storage Location + Product + Packaging + Batch, exactly as
-- the requirement specifies.
CREATE TABLE inventory_balances (
    id                          BIGSERIAL PRIMARY KEY,
    factory_id                  BIGINT      NOT NULL REFERENCES factories (id),
    storage_location_id         BIGINT      NOT NULL REFERENCES storage_locations (id),
    product_id                  BIGINT      NOT NULL REFERENCES products (id),
    packaging_type_id           BIGINT      NOT NULL REFERENCES packaging_types (id),
    batch_no                    TEXT        NOT NULL DEFAULT '',

    package_quantity            NUMERIC(18, 3) NOT NULL DEFAULT 0,
    weight_quantity             NUMERIC(18, 3) NOT NULL DEFAULT 0,
    weight_uom_id               BIGINT      NOT NULL REFERENCES uoms (id),

    -- Committed to a sales order / remelt order but not yet issued.
    reserved_package_quantity   NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (reserved_package_quantity >= 0),
    reserved_weight_quantity    NUMERIC(18, 3) NOT NULL DEFAULT 0 CHECK (reserved_weight_quantity >= 0),

    last_movement_date          DATE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by                  TEXT        NOT NULL DEFAULT 'SYSTEM',
    version                     INTEGER     NOT NULL DEFAULT 1,

    UNIQUE (storage_location_id, product_id, packaging_type_id, batch_no)
);

CREATE INDEX inventory_balances_by_location
    ON inventory_balances (factory_id, storage_location_id);

-- Available = on hand - reserved, on both axes.
CREATE VIEW inventory_available AS
SELECT
    b.id,
    b.factory_id,
    b.storage_location_id,
    b.product_id,
    b.packaging_type_id,
    b.batch_no,
    b.package_quantity,
    b.weight_quantity,
    b.reserved_package_quantity,
    b.reserved_weight_quantity,
    b.package_quantity - b.reserved_package_quantity AS available_package_quantity,
    b.weight_quantity  - b.reserved_weight_quantity  AS available_weight_quantity,
    b.weight_uom_id
FROM inventory_balances b;

COMMIT;
