-- Inter-warehouse transfers and cycle counts.
CREATE TABLE transfers (
    id                BIGINT      NOT NULL AUTO_INCREMENT,
    from_warehouse_id BIGINT      NOT NULL,
    to_warehouse_id   BIGINT      NOT NULL,
    status            VARCHAR(32) NOT NULL,
    created_at        DATETIME(6) NOT NULL,
    shipped_at        DATETIME(6) NULL,
    received_at       DATETIME(6) NULL,
    PRIMARY KEY (id),
    CONSTRAINT fk_transfers_from FOREIGN KEY (from_warehouse_id) REFERENCES warehouses (id),
    CONSTRAINT fk_transfers_to FOREIGN KEY (to_warehouse_id) REFERENCES warehouses (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE transfer_lines (
    id           BIGINT NOT NULL AUTO_INCREMENT,
    transfer_id  BIGINT NOT NULL,
    product_id   BIGINT NOT NULL,
    quantity     BIGINT NOT NULL,
    qty_shipped  BIGINT NOT NULL DEFAULT 0,
    qty_received BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    KEY ix_transfer_lines_transfer (transfer_id),
    CONSTRAINT fk_transfer_lines_transfer FOREIGN KEY (transfer_id) REFERENCES transfers (id),
    CONSTRAINT ck_transfer_lines_qty CHECK (quantity > 0 AND qty_received <= qty_shipped)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- What was shipped per lot (part_kind = 'lot') and per cost (part_kind = 'cost').
CREATE TABLE transfer_line_parts (
    id               BIGINT      NOT NULL AUTO_INCREMENT,
    transfer_line_id BIGINT      NOT NULL,
    part_kind        VARCHAR(8)  NOT NULL,
    lot_id           BIGINT      NULL,
    unit_cost        BIGINT      NOT NULL DEFAULT 0,
    received_at      DATETIME(6) NOT NULL,
    quantity         BIGINT      NOT NULL,
    qty_received     BIGINT      NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    KEY ix_transfer_line_parts_line (transfer_line_id),
    CONSTRAINT fk_transfer_line_parts_line FOREIGN KEY (transfer_line_id) REFERENCES transfer_lines (id),
    CONSTRAINT ck_transfer_line_parts_kind CHECK (part_kind IN ('lot', 'cost'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE cycle_counts (
    id           BIGINT       NOT NULL AUTO_INCREMENT,
    location_id  BIGINT       NOT NULL,
    warehouse_id BIGINT       NOT NULL,
    status       VARCHAR(16)  NOT NULL,
    reason       VARCHAR(16)  NOT NULL DEFAULT '',
    note         VARCHAR(255) NOT NULL DEFAULT '',
    created_at   DATETIME(6)  NOT NULL,
    posted_at    DATETIME(6)  NULL,
    PRIMARY KEY (id),
    KEY ix_cycle_counts_location (location_id),
    CONSTRAINT fk_cycle_counts_location FOREIGN KEY (location_id) REFERENCES locations (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE cycle_count_lines (
    id          BIGINT NOT NULL AUTO_INCREMENT,
    count_id    BIGINT NOT NULL,
    product_id  BIGINT NOT NULL,
    lot_id      BIGINT NULL,
    lot_key     BIGINT AS (IFNULL(lot_id, 0)) STORED,
    system_qty  BIGINT NOT NULL,
    counted_qty BIGINT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_cycle_count_lines (count_id, product_id, lot_key),
    CONSTRAINT fk_cycle_count_lines_count FOREIGN KEY (count_id) REFERENCES cycle_counts (id),
    CONSTRAINT ck_cycle_count_lines_qty CHECK (system_qty >= 0 AND (counted_qty IS NULL OR counted_qty >= 0))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
