-- FIFO cost layers. unit_cost is in IDR minor units per base unit.
CREATE TABLE cost_layers (
    id            BIGINT      NOT NULL AUTO_INCREMENT,
    product_id    BIGINT      NOT NULL,
    warehouse_id  BIGINT      NOT NULL,
    unit_cost     BIGINT      NOT NULL,
    qty_received  BIGINT      NOT NULL,
    qty_remaining BIGINT      NOT NULL,
    source_type   VARCHAR(32) NOT NULL,
    source_id     BIGINT      NOT NULL,
    received_at   DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY ix_cost_layers_fifo (product_id, warehouse_id, received_at, id),
    CONSTRAINT fk_cost_layers_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT ck_cost_layers_qty CHECK (unit_cost >= 0 AND qty_remaining >= 0 AND qty_remaining <= qty_received)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Audit trail of which layers each outbound document consumed.
CREATE TABLE cost_consumptions (
    id             BIGINT      NOT NULL AUTO_INCREMENT,
    layer_id       BIGINT      NOT NULL,
    quantity       BIGINT      NOT NULL,
    unit_cost      BIGINT      NOT NULL,
    reference_type VARCHAR(32) NOT NULL,
    reference_id   BIGINT      NOT NULL,
    created_at     DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    KEY ix_cost_consumptions_ref (reference_type, reference_id),
    CONSTRAINT fk_cost_consumptions_layer FOREIGN KEY (layer_id) REFERENCES cost_layers (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
