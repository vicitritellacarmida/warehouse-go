package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// IdempotencyStore persists Idempotency-Key records. *store.Store
// implements it.
type IdempotencyStore interface {
	BeginIdempotent(ctx context.Context, key, method, path, hash string) (store.IdempotencyRecord, bool, error)
	CompleteIdempotent(ctx context.Context, key, method, path string, status int, body []byte) error
	ReleaseIdempotent(ctx context.Context, key, method, path string) error
}

const maxIdempotencyKeyLen = 128

// recorder captures a response so it can be stored for replay.
type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}

// idempotent makes a write handler safe to retry. When the request has an
// Idempotency-Key header:
//
//   - the first request with the key runs normally and its response is
//     stored (server errors are not stored, so they can be retried);
//   - a later request with the same key, method, path and body gets the
//     stored response again, with the header Idempotent-Replayed: true;
//   - a later request with the same key but a different body gets 422
//     idempotency_key_reused;
//   - a request that arrives while the first is still running gets 409
//     idempotency_in_progress.
//
// Requests without the header are not affected.
func (a *API) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" || a.idem == nil {
			next(w, r)
			return
		}
		if len(key) > maxIdempotencyKeyLen {
			writeError(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must be at most 128 characters", nil)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_body", "could not read request body", nil)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		path := r.URL.Path

		prev, created, err := a.idem.BeginIdempotent(r.Context(), key, r.Method, path, hash)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		if !created {
			switch {
			case prev.RequestHash != hash:
				writeError(w, r, http.StatusUnprocessableEntity, domain.CodeIdempotencyMismatch,
					"this Idempotency-Key was already used with a different request body", nil)
			case prev.StatusCode == 0:
				writeError(w, r, http.StatusConflict, domain.CodeIdempotencyInProgress,
					"a request with this Idempotency-Key is still being processed", nil)
			default:
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Idempotent-Replayed", "true")
				w.WriteHeader(prev.StatusCode)
				_, _ = w.Write(prev.Body)
			}
			return
		}

		rec := &recorder{ResponseWriter: w}
		next(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		ctx := context.WithoutCancel(r.Context())
		if rec.status >= 500 {
			err = a.idem.ReleaseIdempotent(ctx, key, r.Method, path)
		} else {
			err = a.idem.CompleteIdempotent(ctx, key, r.Method, path, rec.status, rec.buf.Bytes())
		}
		if err != nil {
			a.log.ErrorContext(ctx, "store idempotency record", slog.String("key", key), slog.Any("error", err))
		}
	}
}
