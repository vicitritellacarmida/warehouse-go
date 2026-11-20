-- Sales orders, allocations and shipments.
CREATE TABLE sales_orders (
    id              BIGINT       NOT NULL AUTO_INCREMENT,
    order_number    VARCHAR(80)  NOT NULL,
    warehouse_id    BIGINT       NOT NULL,
    customer        VARCHAR(255) NOT NULL DEFAULT '',
    status          VARCHAR(16)  NOT NULL,
    backorder_of_id BIGINT       NULL,
    created_at      DATETIME(6)  NOT NULL,
    updated_at      DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_sales_orders_number (order_number),
    KEY ix_sales_orders_backorder (backorder_of_id),
    CONSTRAINT fk_sales_orders_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE order_lines (
    id              BIGINT      NOT NULL AUTO_INCREMENT,
    order_id        BIGINT      NOT NULL,
    product_id      BIGINT      NOT NULL,
    quantity        BIGINT      NOT NULL,
    qty_allocated   BIGINT      NOT NULL DEFAULT 0,
    qty_backordered BIGINT      NOT NULL DEFAULT 0,
    qty_picked      BIGINT      NOT NULL DEFAULT 0,
    qty_shipped     BIGINT      NOT NULL DEFAULT 0,
    status          VARCHAR(16) NOT NULL,
    PRIMARY KEY (id),
    KEY ix_order_lines_order (order_id),
    CONSTRAINT fk_order_lines_order FOREIGN KEY (order_id) REFERENCES sales_orders (id),
    CONSTRAINT fk_order_lines_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT ck_order_lines_qty CHECK (quantity > 0 AND qty_allocated + qty_backordered <= quantity)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE allocations (
    id            BIGINT      NOT NULL AUTO_INCREMENT,
    order_id      BIGINT      NOT NULL,
    order_line_id BIGINT      NOT NULL,
    product_id    BIGINT      NOT NULL,
    stock_item_id BIGINT      NOT NULL,
    location_id   BIGINT      NOT NULL,
    lot_id        BIGINT      NULL,
    quantity      BIGINT      NOT NULL,
    status        VARCHAR(16) NOT NULL,
    created_at    DATETIME(6) NOT NULL,
    updated_at    DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY ix_allocations_order (order_id),
    KEY ix_allocations_stock (stock_item_id),
    CONSTRAINT fk_allocations_line FOREIGN KEY (order_line_id) REFERENCES order_lines (id),
    CONSTRAINT fk_allocations_stock FOREIGN KEY (stock_item_id) REFERENCES stock_items (id),
    CONSTRAINT ck_allocations_qty CHECK (quantity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE shipments (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    order_id   BIGINT      NOT NULL,
    total_cogs BIGINT      NOT NULL DEFAULT 0,
    shipped_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_shipments_order (order_id),
    CONSTRAINT fk_shipments_order FOREIGN KEY (order_id) REFERENCES sales_orders (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE shipment_lines (
    id            BIGINT NOT NULL AUTO_INCREMENT,
    shipment_id   BIGINT NOT NULL,
    order_line_id BIGINT NOT NULL,
    product_id    BIGINT NOT NULL,
    lot_id        BIGINT NULL,
    quantity      BIGINT NOT NULL,
    cogs          BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_shipment_lines_shipment (shipment_id),
    CONSTRAINT fk_shipment_lines_shipment FOREIGN KEY (shipment_id) REFERENCES shipments (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
