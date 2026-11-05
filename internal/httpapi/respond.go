package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// ErrorBody is the JSON shape of every error response:
//
//	{"error": {"code": "insufficient_stock", "message": "...", "details": {...}}}
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable code, a human message and optional details.
type ErrorDetail struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// listBody wraps list responses so fields can be added later.
type listBody[T any] struct {
	Data []T `json:"data"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeList[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, listBody[T]{Data: items})
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string, details map[string]any) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{
		Code: code, Message: msg, Details: details, RequestID: w.Header().Get("X-Request-ID"),
	}})
}

// statusFor maps a domain error kind to an HTTP status.
func statusFor(k domain.Kind) int {
	switch k {
	case domain.KindInvalid:
		return http.StatusUnprocessableEntity
	case domain.KindNotFound:
		return http.StatusNotFound
	case domain.KindConflict:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// fail writes the error response for err. Domain errors keep their code;
// anything else is logged and reported as a generic internal error.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var de *domain.Error
	if errors.As(err, &de) {
		writeError(w, r, statusFor(de.Kind), de.Code, de.Message, de.Details)
		return
	}
	var be *badRequest
	if errors.As(err, &be) {
		writeError(w, r, http.StatusBadRequest, be.code, be.msg, nil)
		return
	}
	a.log.ErrorContext(r.Context(), "request failed", slog.String("path", r.URL.Path), slog.Any("error", err))
	writeError(w, r, http.StatusInternalServerError, "internal", "internal server error", nil)
}

// badRequest is a malformed request (bad JSON, bad path or query value).
type badRequest struct{ code, msg string }

func (e *badRequest) Error() string { return e.msg }

// errEmptyBody is returned by decode when the body is empty.
var errEmptyBody = &badRequest{"invalid_json", "request body is empty"}

// decode reads a JSON body into v, rejecting unknown fields and trailing
// data.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var de *domain.Error
		if errors.As(err, &de) {
			return de
		}
		if errors.Is(err, io.EOF) {
			return errEmptyBody
		}
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return &badRequest{"body_too_large", "request body is too large"}
		}
		return &badRequest{"invalid_json", "invalid JSON body: " + err.Error()}
	}
	if dec.More() {
		return &badRequest{"invalid_json", "request body has trailing data"}
	}
	return nil
}

// pathID parses a positive integer path value.
func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, &badRequest{"invalid_path", fmt.Sprintf("%s must be a positive integer", name)}
	}
	return id, nil
}

// queryInt parses an optional non-negative integer query value.
func queryInt(r *http.Request, name string) (int64, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, &badRequest{"invalid_query", fmt.Sprintf("%s must be a non-negative integer", name)}
	}
	return n, nil
}
