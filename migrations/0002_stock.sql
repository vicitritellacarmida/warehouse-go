-- Lots, stock rows and the append-only movement ledger.
CREATE TABLE lots (
    id          BIGINT      NOT NULL AUTO_INCREMENT,
    product_id  BIGINT      NOT NULL,
    lot_number  VARCHAR(64) NOT NULL,
    expiry_date DATE        NULL,
    created_at  DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_lots (product_id, lot_number),
    KEY ix_lots_expiry (expiry_date),
    CONSTRAINT fk_lots_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- One row per product + location + lot. lot_key turns a NULL lot into 0 so
-- the unique key also covers products without lots.
CREATE TABLE stock_items (
    id           BIGINT      NOT NULL AUTO_INCREMENT,
    product_id   BIGINT      NOT NULL,
    warehouse_id BIGINT      NOT NULL,
    location_id  BIGINT      NOT NULL,
    lot_id       BIGINT      NULL,
    lot_key      BIGINT      AS (IFNULL(lot_id, 0)) STORED,
    on_hand      BIGINT      NOT NULL DEFAULT 0,
    reserved     BIGINT      NOT NULL DEFAULT 0,
    received_at  DATETIME(6) NOT NULL,
    updated_at   DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_stock_items (location_id, product_id, lot_key),
    KEY ix_stock_items_product (product_id, warehouse_id),
    CONSTRAINT fk_stock_items_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT fk_stock_items_location FOREIGN KEY (location_id) REFERENCES locations (id),
    CONSTRAINT fk_stock_items_lot FOREIGN KEY (lot_id) REFERENCES lots (id),
    CONSTRAINT ck_stock_items_qty CHECK (on_hand >= 0 AND reserved >= 0 AND reserved <= on_hand)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE stock_movements (
    id               BIGINT       NOT NULL AUTO_INCREMENT,
    movement_type    VARCHAR(16)  NOT NULL,
    product_id       BIGINT       NOT NULL,
    warehouse_id     BIGINT       NOT NULL,
    lot_id           BIGINT       NULL,
    from_location_id BIGINT       NULL,
    to_location_id   BIGINT       NULL,
    quantity         BIGINT       NOT NULL,
    reference_type   VARCHAR(32)  NOT NULL,
    reference_id     BIGINT       NOT NULL,
    note             VARCHAR(255) NOT NULL DEFAULT '',
    created_at       DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    KEY ix_movements_product (product_id, id),
    KEY ix_movements_warehouse (warehouse_id, id),
    KEY ix_movements_from (from_location_id, id),
    KEY ix_movements_to (to_location_id, id),
    KEY ix_movements_reference (reference_type, reference_id),
    CONSTRAINT ck_movements_qty CHECK (quantity > 0),
    CONSTRAINT ck_movements_side CHECK (from_location_id IS NOT NULL OR to_location_id IS NOT NULL)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TRIGGER trg_stock_movements_no_update BEFORE UPDATE ON stock_movements
FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'stock_movements is append-only';

CREATE TRIGGER trg_stock_movements_no_delete BEFORE DELETE ON stock_movements
FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'stock_movements is append-only';
