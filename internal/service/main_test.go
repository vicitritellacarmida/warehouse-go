package service_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/service"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
	"github.com/vicitritellacarmida/warehouse-go/internal/testdb"
)

var (
	testStore *store.Store
	skipMsg   string
	seq       atomic.Int64
)

func TestMain(m *testing.M) {
	if skipMsg = testdb.SkipReason(); skipMsg != "" {
		os.Exit(m.Run())
	}
	db, err := testdb.Open("service")
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration test setup:", err)
		os.Exit(1)
	}
	testStore = store.New(db.DB)
	code := m.Run()
	if err := db.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "drop test database:", err)
	}
	os.Exit(code)
}

// env is a per-test fixture with one warehouse and a set of locations.
type env struct {
	t   *testing.T
	ctx context.Context
	svc *service.Service
	wh  *domain.Warehouse
	loc map[string]*domain.Location
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if skipMsg != "" {
		t.Skip(skipMsg)
	}
	t.Parallel()
	e := &env{t: t, ctx: context.Background(), svc: service.New(testStore, service.Options{DefaultToleranceBP: 500})}
	e.wh, e.loc = e.warehouse()
	return e
}

func uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, seq.Add(1)) }

// warehouse creates a warehouse with locations RCV, A1, A2, P1, SHIP, QUAR.
func (e *env) warehouse() (*domain.Warehouse, map[string]*domain.Location) {
	e.t.Helper()
	wh, err := e.svc.CreateWarehouse(e.ctx, service.CreateWarehouseInput{Code: uniq("WH"), Name: "Test warehouse"})
	e.must(err)
	locs := map[string]*domain.Location{}
	for code, typ := range map[string]domain.LocationType{
		"RCV": domain.LocationReceiving, "A1": domain.LocationStorage, "A2": domain.LocationStorage,
		"P1": domain.LocationPicking, "SHIP": domain.LocationShipping, "QUAR": domain.LocationQuarantine,
	} {
		l, err := e.svc.CreateLocation(e.ctx, wh.ID, service.CreateLocationInput{Code: code, Type: typ})
		e.must(err)
		locs[code] = l
	}
	return wh, locs
}

func (e *env) product(mode domain.TrackingMode) *domain.Product {
	e.t.Helper()
	p, err := e.svc.CreateProduct(e.ctx, service.CreateProductInput{
		SKU: uniq("SKU"), Name: "Test product", BaseUOM: "PCS", TrackingMode: mode,
		Units: []domain.Unit{{UOM: "BOX", Factor: 12}},
	})
	e.must(err)
	return p
}

// stock receives qty units of p at unitCost into RCV of wh and puts them
// away to the named location. It returns the lot id (nil when untracked).
func (e *env) stock(wh *domain.Warehouse, locs map[string]*domain.Location, p *domain.Product, to string, qty, unitCost int64, lot string, expiry *domain.Date) *int64 {
	e.t.Helper()
	r, err := e.svc.CreateReceipt(e.ctx, service.CreateReceiptInput{
		WarehouseID: wh.ID, Reference: uniq("PO"), Supplier: "Sample Supplier",
		Lines: []service.CreateReceiptLine{{ProductID: p.ID, Quantity: qty, UnitCost: unitCost}},
	})
	e.must(err)
	_, err = e.svc.Receive(e.ctx, r.ID, service.ReceiveInput{LocationID: locs["RCV"].ID, Lines: []service.ReceiveLine{
		{LineID: r.Lines[0].ID, Quantity: qty, LotNumber: lot, ExpiryDate: expiry},
	}})
	e.must(err)
	var lotID *int64
	if lot != "" {
		rows, err := e.svc.StockReport(e.ctx, store.StockFilter{ProductID: p.ID, LocationID: locs["RCV"].ID, GroupBy: domain.GroupByLocation})
		e.must(err)
		for _, row := range rows {
			if row.LotNumber == lot {
				lotID = row.LotID
			}
		}
	}
	_, err = e.svc.Putaway(e.ctx, service.MoveInput{
		ProductID: p.ID, LotID: lotID, FromLocationID: locs["RCV"].ID, ToLocationID: locs[to].ID, Quantity: qty,
	})
	e.must(err)
	return lotID
}

func (e *env) order(p *domain.Product, qty int64) *service.OrderView {
	e.t.Helper()
	o, err := e.svc.CreateOrder(e.ctx, service.CreateOrderInput{
		OrderNumber: uniq("SO"), WarehouseID: e.wh.ID, Customer: "Sample Customer",
		Lines: []service.CreateOrderLine{{ProductID: p.ID, Quantity: qty}},
	})
	e.must(err)
	return o
}

// onHand returns total on hand and reserved of a product in a location
// (0 location means the whole fixture warehouse).
func (e *env) onHand(p *domain.Product, locationID int64) (onHand, reserved, available int64) {
	e.t.Helper()
	rows, err := e.svc.StockReport(e.ctx, store.StockFilter{ProductID: p.ID, WarehouseID: e.wh.ID, LocationID: locationID, GroupBy: domain.GroupByWarehouse})
	e.must(err)
	for _, r := range rows {
		onHand += r.OnHand
		reserved += r.Reserved
		available += r.Available
	}
	return
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

// wantCode fails unless err is a domain error with the given code.
func (e *env) wantCode(err error, code string) {
	e.t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != code {
		e.t.Fatalf("got error %v, want code %s", err, code)
	}
}

// assertConsistent checks the stock rows against the ledger and the cost
// layers across the whole database.
func (e *env) assertConsistent() {
	e.t.Helper()
	rec, err := e.svc.Reconcile(e.ctx)
	e.must(err)
	if !rec.Consistent() {
		e.t.Fatalf("inventory is inconsistent: %+v", rec)
	}
}

func dateIn(days int) *domain.Date {
	d := domain.DateOf(time.Now()).AddDays(days)
	return &d
}
