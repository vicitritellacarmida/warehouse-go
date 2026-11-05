package service_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/service"
)

// TestConcurrentAllocationNeverOversells starts many allocations at once
// against stock spread over several rows. The total reserved must equal
// exactly the stock that existed, never more.
func TestConcurrentAllocationNeverOversells(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingNone)
	e.stock(e.wh, e.loc, p, "A1", 40, 100, "", nil)
	e.stock(e.wh, e.loc, p, "A2", 35, 100, "", nil)
	e.stock(e.wh, e.loc, p, "P1", 25, 100, "", nil)

	const orders = 30
	const perOrder = 7 // 30 x 7 = 210 requested, 100 available
	ids := make([]int64, orders)
	for i := range ids {
		ids[i] = e.order(p, perOrder).ID
	}

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		allocated int64
		failures  int
	)
	start := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			<-start
			o, err := e.svc.AllocateOrder(e.ctx, id)
			mu.Lock()
			defer mu.Unlock()
			var de *domain.Error
			switch {
			case err == nil:
				allocated += o.Lines[0].QtyAllocated
			case errors.As(err, &de) && de.Code == domain.CodeInsufficientStock:
				failures++
			default:
				t.Errorf("order %d: unexpected error %v", id, err)
			}
		}(id)
	}
	close(start)
	wg.Wait()

	if allocated != 100 {
		t.Fatalf("allocated %d units, want exactly 100", allocated)
	}
	on, reserved, avail := e.onHand(p, 0)
	if on != 100 || reserved != 100 || avail != 0 {
		t.Fatalf("on hand %d reserved %d available %d", on, reserved, avail)
	}
	// 100 / 7 = 14 full orders, 1 partial order with a backorder, the rest fail.
	if failures != orders-15 {
		t.Errorf("failures = %d, want %d", failures, orders-15)
	}
	e.assertConsistent()
}

// TestConcurrentFulfilment picks, packs and ships many orders in parallel
// (they share stock rows and the shipping bin) and then checks that the
// ledger, stock rows and cost layers still agree.
func TestConcurrentFulfilment(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingLotExpiry)
	e.stock(e.wh, e.loc, p, "A1", 60, 1_000, "F1", dateIn(30))
	e.stock(e.wh, e.loc, p, "A2", 60, 1_200, "F2", dateIn(60))

	const orders = 20
	ids := make([]int64, orders)
	for i := range ids {
		o := e.order(p, 5)
		_, err := e.svc.AllocateOrder(e.ctx, o.ID)
		e.must(err)
		ids[i] = o.ID
	}

	var wg sync.WaitGroup
	errs := make(chan error, orders)
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if _, err := e.svc.PickOrder(e.ctx, id, service.PickInput{ShippingLocationID: e.loc["SHIP"].ID}); err != nil {
				errs <- err
				return
			}
			if _, err := e.svc.PackOrder(e.ctx, id); err != nil {
				errs <- err
				return
			}
			if _, err := e.svc.ShipOrder(e.ctx, id); err != nil {
				errs <- err
			}
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	on, reserved, _ := e.onHand(p, 0)
	if on != 20 || reserved != 0 {
		t.Fatalf("on hand %d reserved %d, want 20 and 0", on, reserved)
	}
	var cogs int64
	for _, id := range ids {
		o, err := e.svc.GetOrder(e.ctx, id)
		e.must(err)
		if o.Status != domain.OrderShipped {
			t.Fatalf("order %d is %s", id, o.Status)
		}
		cogs += o.Shipment.TotalCOGS
	}
	// 100 units shipped from layers 60 @ 1000 and 60 @ 1200, oldest first.
	if cogs != 60*1_000+40*1_200 {
		t.Fatalf("total COGS = %d", cogs)
	}
	e.assertConsistent()
}
