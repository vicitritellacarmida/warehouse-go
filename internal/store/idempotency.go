package store

import (
	"context"
	"time"
)

// IdempotencyRecord is a stored request/response pair for one key.
// StatusCode 0 means the first request is still in progress.
type IdempotencyRecord struct {
	Key         string
	Method      string
	Path        string
	RequestHash string
	StatusCode  int
	Body        []byte
}

// BeginIdempotent tries to claim a key. If the key is new it is stored as
// in progress and created is true. Otherwise the existing record is
// returned so the caller can replay it or reject the request.
func (s *Store) BeginIdempotent(ctx context.Context, key, method, path, hash string) (rec IdempotencyRecord, created bool, err error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT IGNORE INTO idempotency_keys (idem_key, method, path, request_hash, status_code, created_at)
		VALUES (?, ?, ?, ?, 0, ?)`, key, method, path, hash, time.Now().UTC())
	if err != nil {
		return rec, false, err
	}
	if rowsAffected(res) == 1 {
		return IdempotencyRecord{Key: key, Method: method, Path: path, RequestHash: hash}, true, nil
	}
	rec = IdempotencyRecord{Key: key, Method: method, Path: path}
	err = s.db.QueryRowContext(ctx, `
		SELECT request_hash, status_code, COALESCE(response_body, '') FROM idempotency_keys
		WHERE idem_key = ? AND method = ? AND path = ?`, key, method, path).
		Scan(&rec.RequestHash, &rec.StatusCode, &rec.Body)
	return rec, false, err
}

// CompleteIdempotent stores the final response for a claimed key.
func (s *Store) CompleteIdempotent(ctx context.Context, key, method, path string, status int, body []byte) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE idempotency_keys SET status_code = ?, response_body = ?
		WHERE idem_key = ? AND method = ? AND path = ?`, status, body, key, method, path)
	return err
}

// ReleaseIdempotent deletes a claimed key so the request can be retried,
// used when the handler failed with a server error.
func (s *Store) ReleaseIdempotent(ctx context.Context, key, method, path string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM idempotency_keys WHERE idem_key = ? AND method = ? AND path = ?`, key, method, path)
	return err
}
