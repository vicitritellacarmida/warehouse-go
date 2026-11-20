-- Stored responses for the Idempotency-Key header. status_code 0 means the
-- first request with this key is still being processed.
CREATE TABLE idempotency_keys (
    idem_key      VARCHAR(128) NOT NULL,
    method        VARCHAR(8)   NOT NULL,
    path          VARCHAR(255) NOT NULL,
    request_hash  CHAR(64)     NOT NULL,
    status_code   INT          NOT NULL DEFAULT 0,
    response_body MEDIUMBLOB   NULL,
    created_at    DATETIME(6)  NOT NULL,
    PRIMARY KEY (idem_key, method, path)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
