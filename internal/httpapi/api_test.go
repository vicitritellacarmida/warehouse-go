package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vicitritellacarmida/warehouse-go/internal/httpapi"
	"github.com/vicitritellacarmida/warehouse-go/internal/service"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
	"github.com/vicitritellacarmida/warehouse-go/internal/testdb"
)

var (
	server  *httptest.Server
	skipMsg string
	seq     atomic.Int64
)

func TestMain(m *testing.M) {
	if skipMsg = testdb.SkipReason(); skipMsg != "" {
		os.Exit(m.Run())
	}
	db, err := testdb.Open("httpapi")
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration test setup:", err)
		os.Exit(1)
	}
	st := store.New(db.DB)
	svc := service.New(st, service.Options{DefaultToleranceBP: 500})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server = httptest.NewServer(httpapi.New(svc, st, logger))
	code := m.Run()
	server.Close()
	if err := db.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "drop test database:", err)
	}
	os.Exit(code)
}

func setup(t *testing.T) {
	t.Helper()
	if skipMsg != "" {
		t.Skip(skipMsg)
	}
	t.Parallel()
}

func uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, seq.Add(1)) }

type response struct {
	Status int
	Header http.Header
	Body   map[string]any
	Raw    []byte
}

// call sends a JSON request. body may be nil, a string (sent as is) or any
// value (JSON encoded). Extra headers are given as key, value pairs.
func call(t *testing.T, method, path string, body any, headers ...string) response {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, server.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{Status: resp.StatusCode, Header: resp.Header, Raw: raw}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// expect fails unless the response has the given status.
func (r response) expect(t *testing.T, status int) response {
	t.Helper()
	if r.Status != status {
		t.Fatalf("status %d, want %d; body %s", r.Status, status, r.Raw)
	}
	return r
}

func (r response) id(t *testing.T) int64 {
	t.Helper()
	v, ok := r.Body["id"].(float64)
	if !ok {
		t.Fatalf("no id in %s", r.Raw)
	}
	return int64(v)
}

func (r response) errCode() string {
	e, _ := r.Body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

func TestEndToEndOrderFlow(t *testing.T) {
	setup(t)
	wh := call(t, "POST", "/api/v1/warehouses", map[string]any{"code": uniq("JKT"), "name": "Sample DC"}).expect(t, 201).id(t)
	loc := map[string]int64{}
	for code, typ := range map[string]string{"RCV-01": "receiving", "A-01": "storage", "SHIP-01": "shipping"} {
		loc[code] = call(t, "POST", fmt.Sprintf("/api/v1/warehouses/%d/locations", wh),
			map[string]any{"code": code, "type": typ}).expect(t, 201).id(t)
	}
	prod := call(t, "POST", "/api/v1/products", map[string]any{
		"sku": uniq("TEA"), "name": "Jasmine tea 330ml", "base_uom": "PCS", "tracking_mode": "lot_expiry",
		"units": []map[string]any{{"uom": "BOX", "factor": 24}},
	}).expect(t, 201)
	pid := prod.id(t)
	if units := prod.Body["units"].([]any); len(units) != 2 {
		t.Fatalf("units = %v", units)
	}

	rc := call(t, "POST", "/api/v1/receipts", map[string]any{
		"warehouse_id": wh, "reference": uniq("PO"), "supplier": "Sample Supplier",
		"lines": []map[string]any{{"product_id": pid, "quantity": 2, "uom": "BOX", "unit_cost": 4_500}},
	}).expect(t, 201)
	line := num(rc.Body["lines"].([]any)[0].(map[string]any)["id"])
	call(t, "POST", fmt.Sprintf("/api/v1/receipts/%d/receive", rc.id(t)), map[string]any{
		"location_id": loc["RCV-01"],
		"lines":       []map[string]any{{"line_id": line, "quantity": 48, "lot_number": "T-1", "expiry_date": "2099-12-31"}},
	}).expect(t, 200)
	call(t, "POST", fmt.Sprintf("/api/v1/receipts/%d/close", rc.id(t)), nil).expect(t, 200)

	stock := call(t, "GET", fmt.Sprintf("/api/v1/stock?product_id=%d&group_by=lot", pid), nil).expect(t, 200)
	rows := stock.Body["data"].([]any)
	lotID := num(rows[0].(map[string]any)["lot_id"])
	call(t, "POST", "/api/v1/putaways", map[string]any{
		"product_id": pid, "lot_id": lotID, "from_location_id": loc["RCV-01"], "to_location_id": loc["A-01"], "quantity": 2, "uom": "BOX",
	}).expect(t, 201)

	o := call(t, "POST", "/api/v1/orders", map[string]any{
		"order_number": uniq("SO"), "warehouse_id": wh, "customer": "Sample Customer",
		"lines": []map[string]any{{"product_id": pid, "quantity": 30}},
	}).expect(t, 201)
	oid := o.id(t)
	base := fmt.Sprintf("/api/v1/orders/%d", oid)
	call(t, "POST", base+"/allocate", nil).expect(t, 200)
	picks := call(t, "GET", base+"/pick-list", nil).expect(t, 200).Body["data"].([]any)
	if len(picks) != 1 || picks[0].(map[string]any)["location_code"] != "A-01" || picks[0].(map[string]any)["lot_number"] != "T-1" {
		t.Fatalf("pick list = %v", picks)
	}
	call(t, "POST", base+"/pick", map[string]any{"shipping_location_id": loc["SHIP-01"]}).expect(t, 200)
	call(t, "POST", base+"/pack", nil).expect(t, 200)
	shipped := call(t, "POST", base+"/ship", nil).expect(t, 200)
	if shipped.Body["status"] != "shipped" || num(shipped.Body["shipment"].(map[string]any)["total_cogs"]) != 30*4_500 {
		t.Fatalf("ship response = %s", shipped.Raw)
	}
	// Shipping again violates the state machine.
	if r := call(t, "POST", base+"/ship", nil).expect(t, 409); r.errCode() != "invalid_transition" {
		t.Fatalf("code = %s", r.errCode())
	}

	mv := call(t, "GET", fmt.Sprintf("/api/v1/movements?product_id=%d&limit=2", pid), nil).expect(t, 200)
	if len(mv.Body["data"].([]any)) != 2 || mv.Body["next_cursor"] == nil {
		t.Fatalf("movements = %s", mv.Raw)
	}
	next := call(t, "GET", fmt.Sprintf("/api/v1/movements?product_id=%d&limit=2&cursor=%s", pid, mv.Body["next_cursor"]), nil).expect(t, 200)
	if len(next.Body["data"].([]any)) != 2 { // receipt, putaway, pick, ship
		t.Fatalf("second page = %s", next.Raw)
	}

	val := call(t, "GET", fmt.Sprintf("/api/v1/reports/valuation?warehouse_id=%d", wh), nil).expect(t, 200)
	if num(val.Body["total_value"]) != 18*4_500 {
		t.Fatalf("valuation = %s", val.Raw)
	}
	rec := call(t, "GET", "/api/v1/reports/reconciliation", nil).expect(t, 200)
	if rec.Body["consistent"] != true {
		t.Fatalf("reconciliation = %s", rec.Raw)
	}
}

func TestIdempotencyKey(t *testing.T) {
	setup(t)
	body := map[string]any{"code": uniq("IDEM"), "name": "Idempotent DC"}
	key := uniq("key")
	first := call(t, "POST", "/api/v1/warehouses", body, "Idempotency-Key", key).expect(t, 201)
	again := call(t, "POST", "/api/v1/warehouses", body, "Idempotency-Key", key).expect(t, 201)
	if again.Header.Get("Idempotent-Replayed") != "true" || !bytes.Equal(first.Raw, again.Raw) {
		t.Fatalf("replay mismatch: %s vs %s", first.Raw, again.Raw)
	}
	// Without the key the same body is a real second request: duplicate code.
	if r := call(t, "POST", "/api/v1/warehouses", body).expect(t, 409); r.errCode() != "duplicate" {
		t.Fatalf("code = %s", r.errCode())
	}
	// Same key, different body.
	other := map[string]any{"code": uniq("IDEM"), "name": "Other"}
	if r := call(t, "POST", "/api/v1/warehouses", other, "Idempotency-Key", key).expect(t, 422); r.errCode() != "idempotency_key_reused" {
		t.Fatalf("code = %s", r.errCode())
	}
	// The same key on another path is independent.
	call(t, "POST", "/api/v1/products", map[string]any{"sku": uniq("S"), "name": "n", "base_uom": "pcs"},
		"Idempotency-Key", key).expect(t, 201)
	// Business errors are stored and replayed too.
	k2 := uniq("key")
	bad := map[string]any{"code": "", "name": "x"}
	call(t, "POST", "/api/v1/warehouses", bad, "Idempotency-Key", k2).expect(t, 422)
	if r := call(t, "POST", "/api/v1/warehouses", bad, "Idempotency-Key", k2).expect(t, 422); r.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("validation error was not replayed")
	}
}

func TestErrorBodies(t *testing.T) {
	setup(t)
	cases := []struct {
		method, path string
		body         any
		status       int
		code         string
	}{
		{"GET", "/api/v1/products/999999999", nil, 404, "product_not_found"},
		{"GET", "/api/v1/products/abc", nil, 400, "invalid_path"},
		{"POST", "/api/v1/products", `{"sku":`, 400, "invalid_json"},
		{"POST", "/api/v1/products", `{"sku":"a","name":"b","base_uom":"c","colour":"red"}`, 400, "invalid_json"},
		{"POST", "/api/v1/products", map[string]any{"sku": "x", "name": "y", "base_uom": "pcs", "tracking_mode": "serial"}, 422, "validation_failed"},
		{"GET", "/api/v1/stock?group_by=shelf", nil, 422, "validation_failed"},
		{"GET", "/api/v1/movements?limit=-1", nil, 400, "invalid_query"},
		{"GET", "/api/v1/nothing", nil, 404, "route_not_found"},
		{"POST", "/api/v1/orders/999999999/allocate", nil, 404, "order_not_found"},
	}
	for _, c := range cases {
		r := call(t, c.method, c.path, c.body)
		if r.Status != c.status || r.errCode() != c.code {
			t.Errorf("%s %s: got %d %q, want %d %q (%s)", c.method, c.path, r.Status, r.errCode(), c.status, c.code, r.Raw)
		}
		if e, _ := r.Body["error"].(map[string]any); e["message"] == "" || e["request_id"] == "" {
			t.Errorf("%s %s: incomplete error body %s", c.method, c.path, r.Raw)
		}
	}
	if r := call(t, "GET", "/healthz", nil); r.Status != 200 || r.Body["status"] != "ok" {
		t.Errorf("healthz = %d %s", r.Status, r.Raw)
	}
}
