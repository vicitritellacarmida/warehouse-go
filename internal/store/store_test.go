package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/migrate"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
	"github.com/vicitritellacarmida/warehouse-go/internal/testdb"
	"github.com/vicitritellacarmida/warehouse-go/migrations"
)

var (
	tdb     *testdb.DB
	skipMsg string
)

func TestMain(m *testing.M) {
	if skipMsg = testdb.SkipReason(); skipMsg != "" {
		os.Exit(m.Run())
	}
	var err error
	if tdb, err = testdb.Open("store"); err != nil {
		fmt.Fprintln(os.Stderr, "integration test setup:", err)
		os.Exit(1)
	}
	code := m.Run()
	if err := tdb.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "drop test database:", err)
	}
	os.Exit(code)
}

func setup(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	if skipMsg != "" {
		t.Skip(skipMsg)
	}
	return store.New(tdb.DB), context.Background()
}

func TestMigrationsAreIdempotent(t *testing.T) {
	_, ctx := setup(t)
	applied, err := migrate.Up(ctx, tdb.DB, migrations.FS, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 0 {
		t.Fatalf("second run applied %v", applied)
	}
	var n int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil || n < 7 {
		t.Fatalf("schema_migrations has %d rows (%v)", n, err)
	}
}

// fixture creates a product, warehouse and storage location.
func fixture(t *testing.T, st *store.Store, ctx context.Context, name string) (pid, wid, lid int64) {
	t.Helper()
	now := time.Now().UTC()
	q := st.Q()
	p := &domain.Product{SKU: name, Name: name, BaseUOM: "PCS", TrackingMode: domain.TrackingNone,
		Units: []domain.Unit{{UOM: "PCS", Factor: 1}}, CreatedAt: now}
	w := &domain.Warehouse{Code: name, Name: name, CreatedAt: now}
	if err := q.CreateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := q.CreateWarehouse(ctx, w); err != nil {
		t.Fatal(err)
	}
	l := &domain.Location{WarehouseID: w.ID, Code: "A", Type: domain.LocationStorage, CreatedAt: now}
	if err := q.CreateLocation(ctx, l); err != nil {
		t.Fatal(err)
	}
	return p.ID, w.ID, l.ID
}

func TestMovementLedgerIsAppendOnly(t *testing.T) {
	st, ctx := setup(t)
	pid, wid, lid := fixture(t, st, ctx, "LEDGER")
	m := &domain.Movement{Type: domain.MoveReceipt, ProductID: pid, WarehouseID: wid, ToLocationID: &lid,
		Quantity: 5, ReferenceType: "test", ReferenceID: 1, CreatedAt: time.Now().UTC()}
	if err := st.Q().InsertMovement(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE stock_movements SET quantity = 6 WHERE id = ?",
		"DELETE FROM stock_movements WHERE id = ?",
	} {
		_, err := tdb.Exec(stmt, m.ID)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q: expected append-only error, got %v", stmt, err)
		}
	}
}

func TestStockRowsAndGuards(t *testing.T) {
	st, ctx := setup(t)
	pid, wid, lid := fixture(t, st, ctx, "GUARD")
	now := time.Now().UTC()
	q := st.Q()
	id1, err := q.EnsureStockItem(ctx, pid, wid, lid, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := q.EnsureStockItem(ctx, pid, wid, lid, nil, now.Add(time.Hour))
	if err != nil || id1 != id2 || id1 == 0 {
		t.Fatalf("EnsureStockItem ids %d, %d (%v)", id1, id2, err)
	}
	if err := q.UpdateStock(ctx, id1, 10, 4, now); err != nil {
		t.Fatal(err)
	}
	insufficient := func(err error) bool {
		var de *domain.Error
		return errors.As(err, &de) && de.Code == domain.CodeInsufficientStock
	}
	// Cannot take reserved units, cannot over-reserve, cannot release more than reserved.
	for _, d := range [][2]int64{{-7, 0}, {0, 7}, {0, -5}, {-11, -4}} {
		if err := q.UpdateStock(ctx, id1, d[0], d[1], now); !insufficient(err) {
			t.Errorf("UpdateStock(%d, %d): got %v", d[0], d[1], err)
		}
	}
	if err := q.UpdateStock(ctx, id1, -6, 0, now); err != nil {
		t.Errorf("taking the 6 free units failed: %v", err)
	}
	items, err := q.LockStockItems(ctx, []int64{id1})
	if err != nil || items[id1].OnHand != 4 || items[id1].Reserved != 4 {
		t.Fatalf("stock row = %+v (%v)", items[id1], err)
	}
}

func TestInTxRollsBack(t *testing.T) {
	st, ctx := setup(t)
	boom := errors.New("boom")
	err := st.InTx(ctx, func(q *store.Queries) error {
		if err := q.CreateWarehouse(ctx, &domain.Warehouse{Code: "ROLLBACK", Name: "x", CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var n int
	if err := tdb.QueryRow("SELECT COUNT(*) FROM warehouses WHERE code = 'ROLLBACK'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("warehouse was committed: %d (%v)", n, err)
	}
}
