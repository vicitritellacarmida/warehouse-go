// Package httpapi exposes the warehouse service over HTTP with JSON bodies.
// It decodes requests, calls the service layer and maps domain errors to a
// consistent error body.
package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/vicitritellacarmida/warehouse-go/internal/service"
)

// maxBodyBytes limits request bodies.
const maxBodyBytes = 1 << 20

// API holds the dependencies of the HTTP handlers.
type API struct {
	svc  *service.Service
	idem IdempotencyStore
	log  *slog.Logger
}

// New builds the HTTP handler with all routes and middleware. idem may be
// nil to disable Idempotency-Key support.
func New(svc *service.Service, idem IdempotencyStore, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	a := &API{svc: svc, idem: idem, log: logger}
	mux := http.NewServeMux()

	read := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }
	write := func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, a.idempotent(h)) }

	read("GET /healthz", a.health)

	write("POST /api/v1/products", a.createProduct)
	read("GET /api/v1/products", a.listProducts)
	read("GET /api/v1/products/{id}", a.getProduct)
	write("POST /api/v1/products/{id}/units", a.addUnit)

	write("POST /api/v1/warehouses", a.createWarehouse)
	read("GET /api/v1/warehouses", a.listWarehouses)
	read("GET /api/v1/warehouses/{id}", a.getWarehouse)
	write("POST /api/v1/warehouses/{id}/locations", a.createLocation)
	read("GET /api/v1/warehouses/{id}/locations", a.listLocations)
	write("PUT /api/v1/warehouses/{id}/reorder-points/{productID}", a.setReorderPoint)

	write("POST /api/v1/receipts", a.createReceipt)
	read("GET /api/v1/receipts/{id}", a.getReceipt)
	write("POST /api/v1/receipts/{id}/receive", a.receive)
	write("POST /api/v1/receipts/{id}/close", a.closeReceipt)
	write("POST /api/v1/receipts/{id}/cancel", a.cancelReceipt)
	write("POST /api/v1/putaways", a.putaway)
	write("POST /api/v1/stock/moves", a.moveStock)

	write("POST /api/v1/orders", a.createOrder)
	read("GET /api/v1/orders/{id}", a.getOrder)
	read("GET /api/v1/orders/{id}/pick-list", a.pickList)
	write("POST /api/v1/orders/{id}/allocate", a.allocateOrder)
	write("POST /api/v1/orders/{id}/pick", a.pickOrder)
	write("POST /api/v1/orders/{id}/pack", a.packOrder)
	write("POST /api/v1/orders/{id}/ship", a.shipOrder)
	write("POST /api/v1/orders/{id}/cancel", a.cancelOrder)

	write("POST /api/v1/transfers", a.createTransfer)
	read("GET /api/v1/transfers/{id}", a.getTransfer)
	write("POST /api/v1/transfers/{id}/ship", a.shipTransfer)
	write("POST /api/v1/transfers/{id}/receive", a.receiveTransfer)
	write("POST /api/v1/transfers/{id}/cancel", a.cancelTransfer)

	write("POST /api/v1/cycle-counts", a.openCount)
	read("GET /api/v1/cycle-counts/{id}", a.getCount)
	write("POST /api/v1/cycle-counts/{id}/lines", a.recordCount)
	write("POST /api/v1/cycle-counts/{id}/post", a.postCount)
	write("POST /api/v1/cycle-counts/{id}/cancel", a.cancelCount)

	read("GET /api/v1/stock", a.stockReport)
	read("GET /api/v1/movements", a.listMovements)
	read("GET /api/v1/reports/expiring", a.expiringReport)
	read("GET /api/v1/reports/low-stock", a.lowStockReport)
	read("GET /api/v1/reports/valuation", a.valuationReport)
	read("GET /api/v1/reports/reconciliation", a.reconciliation)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "route_not_found", fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path), nil)
	})

	return a.middleware(mux)
}

// statusWriter remembers the status code for access logs.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// middleware adds a request id, a body size limit, panic recovery and an
// access log line per request.
func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" || len(reqID) > 64 {
			reqID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", reqID)
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				a.log.ErrorContext(r.Context(), "panic", slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
				writeError(sw, r, http.StatusInternalServerError, "internal", "internal server error", nil)
			}
			a.log.InfoContext(r.Context(), "request",
				slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Int("status", sw.status),
				slog.Duration("duration", time.Since(start)), slog.String("request_id", reqID))
		}()
		next.ServeHTTP(sw, r)
	})
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Store().DB().PingContext(r.Context()); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "database_unavailable", "database is not reachable", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
