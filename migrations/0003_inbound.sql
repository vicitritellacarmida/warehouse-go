-- Purchase receipts.
CREATE TABLE receipts (
    id           BIGINT       NOT NULL AUTO_INCREMENT,
    warehouse_id BIGINT       NOT NULL,
    reference    VARCHAR(64)  NOT NULL,
    supplier     VARCHAR(255) NOT NULL DEFAULT '',
    status       VARCHAR(16)  NOT NULL,
    tolerance_bp BIGINT       NOT NULL DEFAULT 0,
    created_at   DATETIME(6)  NOT NULL,
    closed_at    DATETIME(6)  NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_receipts_reference (warehouse_id, reference),
    CONSTRAINT fk_receipts_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE receipt_lines (
    id           BIGINT NOT NULL AUTO_INCREMENT,
    receipt_id   BIGINT NOT NULL,
    product_id   BIGINT NOT NULL,
    expected_qty BIGINT NOT NULL,
    received_qty BIGINT NOT NULL DEFAULT 0,
    unit_cost    BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_receipt_lines (receipt_id, product_id),
    CONSTRAINT fk_receipt_lines_receipt FOREIGN KEY (receipt_id) REFERENCES receipts (id),
    CONSTRAINT fk_receipt_lines_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT ck_receipt_lines_qty CHECK (expected_qty > 0 AND received_qty >= 0 AND unit_cost >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
