-- Catalog, warehouses and locations.
CREATE TABLE products (
    id            BIGINT       NOT NULL AUTO_INCREMENT,
    sku           VARCHAR(64)  NOT NULL,
    name          VARCHAR(255) NOT NULL,
    base_uom      VARCHAR(16)  NOT NULL,
    tracking_mode VARCHAR(16)  NOT NULL DEFAULT 'none',
    created_at    DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_products_sku (sku),
    CONSTRAINT ck_products_tracking CHECK (tracking_mode IN ('none', 'lot', 'lot_expiry'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE product_units (
    id         BIGINT      NOT NULL AUTO_INCREMENT,
    product_id BIGINT      NOT NULL,
    uom        VARCHAR(16) NOT NULL,
    factor     BIGINT      NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_product_units (product_id, uom),
    CONSTRAINT fk_product_units_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT ck_product_units_factor CHECK (factor > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE warehouses (
    id         BIGINT       NOT NULL AUTO_INCREMENT,
    code       VARCHAR(32)  NOT NULL,
    name       VARCHAR(255) NOT NULL,
    created_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_warehouses_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- open_count_id is set while a cycle count is open on the location.
CREATE TABLE locations (
    id            BIGINT      NOT NULL AUTO_INCREMENT,
    warehouse_id  BIGINT      NOT NULL,
    code          VARCHAR(64) NOT NULL,
    location_type VARCHAR(16) NOT NULL,
    capacity      BIGINT      NULL,
    open_count_id BIGINT      NULL,
    created_at    DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_locations_code (warehouse_id, code),
    CONSTRAINT fk_locations_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id),
    CONSTRAINT ck_locations_type CHECK (location_type IN ('receiving', 'storage', 'picking', 'shipping', 'quarantine')),
    CONSTRAINT ck_locations_capacity CHECK (capacity IS NULL OR capacity > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE reorder_points (
    product_id    BIGINT      NOT NULL,
    warehouse_id  BIGINT      NOT NULL,
    reorder_point BIGINT      NOT NULL,
    updated_at    DATETIME(6) NOT NULL,
    PRIMARY KEY (product_id, warehouse_id),
    CONSTRAINT fk_reorder_points_product FOREIGN KEY (product_id) REFERENCES products (id),
    CONSTRAINT fk_reorder_points_warehouse FOREIGN KEY (warehouse_id) REFERENCES warehouses (id),
    CONSTRAINT ck_reorder_points_qty CHECK (reorder_point >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
