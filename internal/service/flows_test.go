package service_test

import (
	"testing"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/service"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

func TestReceiptToleranceAndClose(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingNone)
	r, err := e.svc.CreateReceipt(e.ctx, service.CreateReceiptInput{
		WarehouseID: e.wh.ID, Reference: uniq("PO"),
		Lines: []service.CreateReceiptLine{{ProductID: p.ID, Quantity: 10, UOM: "BOX", UnitCost: 2_500}},
	})
	e.must(err)
	if r.Lines[0].ExpectedQty != 120 || r.ToleranceBP != 500 {
		t.Fatalf("expected 120 base units with 5%% tolerance, got %+v", r)
	}
	line := r.Lines[0].ID
	rcv := e.loc["RCV"].ID

	// 120 + 5% = 126 is the maximum.
	_, err = e.svc.Receive(e.ctx, r.ID, service.ReceiveInput{LocationID: rcv, Lines: []service.ReceiveLine{{LineID: line, Quantity: 127}}})
	e.wantCode(err, domain.CodeOverReceipt)

	// Receiving into a storage bin is not allowed.
	_, err = e.svc.Receive(e.ctx, r.ID, service.ReceiveInput{LocationID: e.loc["A1"].ID, Lines: []service.ReceiveLine{{LineID: line, Quantity: 1}}})
	e.wantCode(err, domain.CodeWrongLocationType)

	r, err = e.svc.Receive(e.ctx, r.ID, service.ReceiveInput{LocationID: rcv, Lines: []service.ReceiveLine{{LineID: line, Quantity: 8, UOM: "BOX"}}})
	e.must(err)
	if r.Status != domain.ReceiptReceiving || r.Lines[0].ReceivedQty != 96 || r.Lines[0].Outcome != domain.OutcomeShort {
		t.Fatalf("unexpected receipt state %+v", r)
	}

	_, err = e.svc.CloseReceipt(e.ctx, r.ID, service.CloseReceiptInput{})
	e.wantCode(err, domain.CodeUnderReceipt)
	r, err = e.svc.CloseReceipt(e.ctx, r.ID, service.CloseReceiptInput{AcceptShort: true})
	e.must(err)
	if r.Status != domain.ReceiptClosed || r.ClosedAt == nil {
		t.Fatalf("receipt not closed: %+v", r)
	}
	_, err = e.svc.Receive(e.ctx, r.ID, service.ReceiveInput{LocationID: rcv, Lines: []service.ReceiveLine{{LineID: line, Quantity: 1}}})
	e.wantCode(err, domain.CodeInvalidTransition)

	if on, _, _ := e.onHand(p, rcv); on != 96 {
		t.Fatalf("on hand in receiving = %d, want 96", on)
	}
	val, err := e.svc.Valuation(e.ctx, e.wh.ID)
	e.must(err)
	if len(val.Rows) != 1 || val.Rows[0].Quantity != 96 || val.Rows[0].Value != 96*2_500 {
		t.Fatalf("valuation = %+v", val)
	}
	e.assertConsistent()
}

func TestLotRules(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingLotExpiry)
	r, err := e.svc.CreateReceipt(e.ctx, service.CreateReceiptInput{
		WarehouseID: e.wh.ID, Reference: uniq("PO"),
		Lines: []service.CreateReceiptLine{{ProductID: p.ID, Quantity: 50, UnitCost: 100}},
	})
	e.must(err)
	in := func(lot string, exp *domain.Date) service.ReceiveInput {
		return service.ReceiveInput{LocationID: e.loc["RCV"].ID, Lines: []service.ReceiveLine{
			{LineID: r.Lines[0].ID, Quantity: 5, LotNumber: lot, ExpiryDate: exp}}}
	}
	_, err = e.svc.Receive(e.ctx, r.ID, in("", nil))
	e.wantCode(err, domain.CodeLotRequired)
	_, err = e.svc.Receive(e.ctx, r.ID, in("L1", nil))
	e.wantCode(err, domain.CodeExpiryRequired)
	_, err = e.svc.Receive(e.ctx, r.ID, in("L1", dateIn(90)))
	e.must(err)
	_, err = e.svc.Receive(e.ctx, r.ID, in("L1", dateIn(91)))
	e.wantCode(err, domain.CodeLotExpiryMismatch)
}

func TestOrderLifecycleFEFOAndCOGS(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingLotExpiry)
	late := e.stock(e.wh, e.loc, p, "A1", 10, 1_000, "LATE", dateIn(200))
	soon := e.stock(e.wh, e.loc, p, "A2", 10, 2_000, "SOON", dateIn(20))
	e.stock(e.wh, e.loc, p, "P1", 50, 3_000, "EXPIRED", dateIn(-1))

	o := e.order(p, 15)
	o, err := e.svc.AllocateOrder(e.ctx, o.ID)
	e.must(err)
	if o.Status != domain.OrderAllocated || o.BackorderID != nil {
		t.Fatalf("order = %+v", o.SalesOrder)
	}
	byLot := map[int64]int64{}
	for _, a := range o.Allocations {
		byLot[*a.LotID] += a.Quantity
	}
	if byLot[*soon] != 10 || byLot[*late] != 5 || len(byLot) != 2 {
		t.Fatalf("FEFO allocation wrong: %v", byLot)
	}
	if _, reserved, _ := e.onHand(p, 0); reserved != 15 {
		t.Fatalf("reserved = %d", reserved)
	}

	// Shipping before packing is not allowed.
	_, err = e.svc.ShipOrder(e.ctx, o.ID)
	e.wantCode(err, domain.CodeInvalidTransition)
	// Picking into a non-shipping location is not allowed.
	_, err = e.svc.PickOrder(e.ctx, o.ID, service.PickInput{ShippingLocationID: e.loc["A1"].ID})
	e.wantCode(err, domain.CodeWrongLocationType)

	tasks, err := e.svc.PickList(e.ctx, o.ID)
	e.must(err)
	if len(tasks) != 2 || tasks[0].LocationCode != "A1" {
		t.Fatalf("pick list = %+v", tasks)
	}

	o, err = e.svc.PickOrder(e.ctx, o.ID, service.PickInput{ShippingLocationID: e.loc["SHIP"].ID})
	e.must(err)
	if on, res, _ := e.onHand(p, e.loc["SHIP"].ID); on != 15 || res != 15 {
		t.Fatalf("shipping bin on hand %d reserved %d", on, res)
	}
	o, err = e.svc.PackOrder(e.ctx, o.ID)
	e.must(err)
	o, err = e.svc.ShipOrder(e.ctx, o.ID)
	e.must(err)
	if o.Status != domain.OrderShipped || o.Lines[0].Status != domain.LineShipped || o.Lines[0].QtyShipped != 15 {
		t.Fatalf("order after ship = %+v", o.SalesOrder)
	}
	// FIFO cost layers by receipt time: 10 @ 1000 (LATE was received first), then 5 @ 2000.
	if o.Shipment == nil || o.Shipment.TotalCOGS != 10*1_000+5*2_000 {
		t.Fatalf("shipment = %+v", o.Shipment)
	}
	if on, res, _ := e.onHand(p, 0); on != 55 || res != 0 {
		t.Fatalf("after ship on hand %d reserved %d", on, res)
	}
	e.assertConsistent()
}

func TestPartialAllocationCreatesBackorder(t *testing.T) {
	e := newEnv(t)
	a := e.product(domain.TrackingNone)
	b := e.product(domain.TrackingNone)
	e.stock(e.wh, e.loc, a, "A1", 6, 500, "", nil)
	e.stock(e.wh, e.loc, a, "A2", 4, 600, "", nil)

	o, err := e.svc.CreateOrder(e.ctx, service.CreateOrderInput{
		OrderNumber: uniq("SO"), WarehouseID: e.wh.ID,
		Lines: []service.CreateOrderLine{{ProductID: a.ID, Quantity: 1, UOM: "BOX"}, {ProductID: b.ID, Quantity: 3}},
	})
	e.must(err)
	o, err = e.svc.AllocateOrder(e.ctx, o.ID)
	e.must(err)
	if o.Status != domain.OrderAllocated || o.BackorderID == nil {
		t.Fatalf("expected allocated with backorder, got %+v", o.SalesOrder)
	}
	if l := o.Lines[0]; l.QtyAllocated != 10 || l.QtyBackordered != 2 || l.Status != domain.LineAllocated {
		t.Fatalf("line a = %+v", l)
	}
	if l := o.Lines[1]; l.QtyAllocated != 0 || l.QtyBackordered != 3 || l.Status != domain.LineBackordered {
		t.Fatalf("line b = %+v", l)
	}
	bo, err := e.svc.GetOrder(e.ctx, *o.BackorderID)
	e.must(err)
	if bo.Status != domain.OrderNew || *bo.BackorderOfID != o.ID || len(bo.Lines) != 2 ||
		bo.Lines[0].Quantity != 2 || bo.Lines[1].Quantity != 3 {
		t.Fatalf("backorder = %+v", bo.SalesOrder)
	}

	// Nothing left: allocating the backorder fails and leaves it new.
	_, err = e.svc.AllocateOrder(e.ctx, bo.ID)
	e.wantCode(err, domain.CodeInsufficientStock)

	// New stock arrives and the backorder can be filled.
	e.stock(e.wh, e.loc, a, "A1", 2, 500, "", nil)
	e.stock(e.wh, e.loc, b, "A1", 3, 900, "", nil)
	bo, err = e.svc.AllocateOrder(e.ctx, bo.ID)
	e.must(err)
	if bo.Status != domain.OrderAllocated || bo.BackorderID != nil {
		t.Fatalf("backorder allocation = %+v", bo.SalesOrder)
	}
	e.assertConsistent()
}

func TestCancelReleasesReservations(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingNone)
	e.stock(e.wh, e.loc, p, "A1", 20, 100, "", nil)

	o1 := e.order(p, 5)
	_, err := e.svc.AllocateOrder(e.ctx, o1.ID)
	e.must(err)
	o2 := e.order(p, 7)
	_, err = e.svc.AllocateOrder(e.ctx, o2.ID)
	e.must(err)
	_, err = e.svc.PickOrder(e.ctx, o2.ID, service.PickInput{ShippingLocationID: e.loc["SHIP"].ID})
	e.must(err)
	if _, res, avail := e.onHand(p, 0); res != 12 || avail != 8 {
		t.Fatalf("reserved %d available %d", res, avail)
	}

	o1v, err := e.svc.CancelOrder(e.ctx, o1.ID)
	e.must(err)
	o2v, err := e.svc.CancelOrder(e.ctx, o2.ID)
	e.must(err)
	if o1v.Status != domain.OrderCancelled || o2v.Lines[0].Status != domain.LineCancelled {
		t.Fatal("orders not cancelled")
	}
	for _, a := range append(o1v.Allocations, o2v.Allocations...) {
		if a.Status != domain.AllocReleased {
			t.Fatalf("allocation %d is %s", a.ID, a.Status)
		}
	}
	if on, res, avail := e.onHand(p, 0); on != 20 || res != 0 || avail != 20 {
		t.Fatalf("on hand %d reserved %d available %d", on, res, avail)
	}
	// Picked units are free stock in SHIP now and can be moved back.
	_, err = e.svc.MoveStock(e.ctx, service.MoveInput{ProductID: p.ID, FromLocationID: e.loc["SHIP"].ID, ToLocationID: e.loc["A1"].ID, Quantity: 7})
	e.must(err)
	_, err = e.svc.CancelOrder(e.ctx, o1.ID)
	e.wantCode(err, domain.CodeInvalidTransition)
	e.assertConsistent()
}

func TestQuarantineIsNeverAvailable(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingNone)
	e.stock(e.wh, e.loc, p, "QUAR", 30, 100, "", nil)
	if on, _, avail := e.onHand(p, 0); on != 30 || avail != 0 {
		t.Fatalf("on hand %d available %d", on, avail)
	}
	o := e.order(p, 1)
	_, err := e.svc.AllocateOrder(e.ctx, o.ID)
	e.wantCode(err, domain.CodeInsufficientStock)

	// Releasing from quarantine makes the stock usable.
	_, err = e.svc.MoveStock(e.ctx, service.MoveInput{ProductID: p.ID, FromLocationID: e.loc["QUAR"].ID, ToLocationID: e.loc["A1"].ID, Quantity: 10})
	e.must(err)
	_, err = e.svc.AllocateOrder(e.ctx, o.ID)
	e.must(err)
	// Reserved stock cannot be moved away.
	_, err = e.svc.MoveStock(e.ctx, service.MoveInput{ProductID: p.ID, FromLocationID: e.loc["A1"].ID, ToLocationID: e.loc["A2"].ID, Quantity: 10})
	e.wantCode(err, domain.CodeInsufficientStock)
}

func TestCapacity(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingNone)
	capacity := int64(15)
	small, err := e.svc.CreateLocation(e.ctx, e.wh.ID, service.CreateLocationInput{Code: "SMALL", Type: domain.LocationStorage, Capacity: &capacity})
	e.must(err)
	e.stock(e.wh, e.loc, p, "A1", 20, 100, "", nil)
	move := service.MoveInput{ProductID: p.ID, FromLocationID: e.loc["A1"].ID, ToLocationID: small.ID, Quantity: 10}
	_, err = e.svc.MoveStock(e.ctx, move)
	e.must(err)
	_, err = e.svc.MoveStock(e.ctx, move)
	e.wantCode(err, domain.CodeCapacityExceeded)
}

func TestTransferWithDiscrepancy(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingLot)
	e.stock(e.wh, e.loc, p, "A1", 10, 1_000, "LA", nil)
	e.stock(e.wh, e.loc, p, "A2", 10, 1_500, "LB", nil)
	dest, destLocs := e.warehouse()

	tr, err := e.svc.CreateTransfer(e.ctx, service.CreateTransferInput{
		FromWarehouseID: e.wh.ID, ToWarehouseID: dest.ID,
		Lines: []service.CreateOrderLine{{ProductID: p.ID, Quantity: 15}},
	})
	e.must(err)
	_, err = e.svc.ReceiveTransfer(e.ctx, tr.ID, service.ReceiveTransferInput{LocationID: destLocs["RCV"].ID})
	e.wantCode(err, domain.CodeInvalidTransition)

	tr, err = e.svc.ShipTransfer(e.ctx, tr.ID)
	e.must(err)
	if tr.Status != domain.TransferInTransit || tr.Lines[0].QtyShipped != 15 || len(tr.Lines[0].Lots) != 2 {
		t.Fatalf("after ship: %+v", tr)
	}
	if on, _, _ := e.onHand(p, 0); on != 5 {
		t.Fatalf("source on hand = %d, want 5", on)
	}
	_, err = e.svc.CancelTransfer(e.ctx, tr.ID)
	e.wantCode(err, domain.CodeInvalidTransition)

	_, err = e.svc.ReceiveTransfer(e.ctx, tr.ID, service.ReceiveTransferInput{LocationID: destLocs["RCV"].ID,
		Lines: []service.ReceiveTransferLine{{LineID: tr.Lines[0].ID, Quantity: 16}}})
	e.wantCode(err, domain.CodeTransferOverReceipt)

	tr, err = e.svc.ReceiveTransfer(e.ctx, tr.ID, service.ReceiveTransferInput{LocationID: destLocs["RCV"].ID,
		Lines: []service.ReceiveTransferLine{{LineID: tr.Lines[0].ID, Quantity: 12}}})
	e.must(err)
	l := tr.Lines[0]
	// Shipped 10 @ 1000 + 5 @ 1500; received 12 fills the first cost part
	// then 2 of the second, so 3 @ 1500 are lost.
	if tr.Status != domain.TransferDiscrepancy || l.QtyReceived != 12 || l.Discrepancy != 3 || l.DiscrepancyValue != 4_500 {
		t.Fatalf("after receive: %+v", l)
	}
	val, err := e.svc.Valuation(e.ctx, dest.ID)
	e.must(err)
	if val.TotalValue != 10*1_000+2*1_500 {
		t.Fatalf("destination valuation = %+v", val)
	}
	rows, err := e.svc.StockReport(e.ctx, store.StockFilter{ProductID: p.ID, WarehouseID: dest.ID, GroupBy: domain.GroupByLot})
	e.must(err)
	if len(rows) != 2 || rows[0].OnHand+rows[1].OnHand != 12 {
		t.Fatalf("destination lots = %+v", rows)
	}
	e.assertConsistent()
}

func TestCycleCountBlocksAndAdjusts(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingLot)
	lot := e.stock(e.wh, e.loc, p, "A1", 20, 700, "C1", nil)
	a1 := e.loc["A1"].ID

	c, err := e.svc.OpenCount(e.ctx, service.OpenCountInput{LocationID: a1})
	e.must(err)
	if len(c.Lines) != 1 || c.Lines[0].SystemQty != 20 {
		t.Fatalf("count snapshot = %+v", c.Lines)
	}
	_, err = e.svc.OpenCount(e.ctx, service.OpenCountInput{LocationID: a1})
	e.wantCode(err, domain.CodeCountAlreadyOpen)

	// Movements in and out of the location are blocked while counting.
	_, err = e.svc.MoveStock(e.ctx, service.MoveInput{ProductID: p.ID, LotID: lot, FromLocationID: a1, ToLocationID: e.loc["A2"].ID, Quantity: 1})
	e.wantCode(err, domain.CodeLocationCounting)
	// So is allocating from it.
	o := e.order(p, 1)
	_, err = e.svc.AllocateOrder(e.ctx, o.ID)
	e.wantCode(err, domain.CodeInsufficientStock)

	_, err = e.svc.PostCount(e.ctx, c.ID, service.PostCountInput{Reason: domain.ReasonDamaged})
	e.wantCode(err, domain.CodeUncountedLines)

	_, err = e.svc.RecordCount(e.ctx, c.ID, service.RecordCountInput{Lines: []service.RecordCountLine{
		{ProductID: p.ID, LotNumber: "C1", Counted: 17},
		{ProductID: p.ID, LotNumber: "C2", Counted: 4},
	}})
	e.must(err)
	_, err = e.svc.PostCount(e.ctx, c.ID, service.PostCountInput{Reason: "because"})
	e.wantCode(err, domain.CodeValidation)

	c, err = e.svc.PostCount(e.ctx, c.ID, service.PostCountInput{Reason: domain.ReasonMiscount, Note: "aisle recount"})
	e.must(err)
	if c.Status != domain.CountPosted || c.Reason != domain.ReasonMiscount || len(c.Lines) != 2 {
		t.Fatalf("posted count = %+v", c)
	}
	if on, _, _ := e.onHand(p, a1); on != 21 {
		t.Fatalf("on hand after count = %d, want 21", on)
	}
	page, err := e.svc.ListMovements(e.ctx, store.MovementFilter{ProductID: p.ID, Type: domain.MoveAdjustment}, "")
	e.must(err)
	if len(page.Data) != 2 || page.Data[0].Note != "miscount: aisle recount" {
		t.Fatalf("adjustments = %+v", page.Data)
	}
	// Loss consumed 3 @ 700, gain added 4 @ latest cost 700.
	val, err := e.svc.Valuation(e.ctx, e.wh.ID)
	e.must(err)
	if val.TotalValue != 21*700 {
		t.Fatalf("valuation = %+v", val)
	}
	// The location is open again.
	_, err = e.svc.MoveStock(e.ctx, service.MoveInput{ProductID: p.ID, LotID: lot, FromLocationID: a1, ToLocationID: e.loc["A2"].ID, Quantity: 1})
	e.must(err)
	e.assertConsistent()
}

func TestReportsAndPagination(t *testing.T) {
	e := newEnv(t)
	p := e.product(domain.TrackingLotExpiry)
	e.stock(e.wh, e.loc, p, "A1", 5, 100, "E-SOON", dateIn(3))
	e.stock(e.wh, e.loc, p, "A2", 5, 100, "E-LATER", dateIn(300))
	e.stock(e.wh, e.loc, p, "A2", 2, 100, "E-OLD", dateIn(-2))

	exp, err := e.svc.ExpiringReport(e.ctx, e.wh.ID, 30)
	e.must(err)
	if len(exp) != 2 || exp[0].LotNumber != "E-OLD" || !exp[0].Expired || exp[1].DaysLeft != 3 {
		t.Fatalf("expiring = %+v", exp)
	}

	e.must(e.svc.SetReorderPoint(e.ctx, e.wh.ID, p.ID, 20))
	low, err := e.svc.LowStockReport(e.ctx, e.wh.ID)
	e.must(err)
	if len(low) != 1 || low[0].Available != 12 || low[0].Shortfall != 8 {
		t.Fatalf("low stock = %+v", low)
	}
	e.must(e.svc.SetReorderPoint(e.ctx, e.wh.ID, p.ID, 5))
	low, err = e.svc.LowStockReport(e.ctx, e.wh.ID)
	e.must(err)
	if len(low) != 0 {
		t.Fatalf("low stock after lowering point = %+v", low)
	}

	// 3 receipts + 3 put-aways = 6 movements, read 4 then 2.
	f := store.MovementFilter{ProductID: p.ID, Limit: 4}
	page, err := e.svc.ListMovements(e.ctx, f, "")
	e.must(err)
	if len(page.Data) != 4 || page.NextCursor == "" {
		t.Fatalf("page 1 = %d rows, cursor %q", len(page.Data), page.NextCursor)
	}
	page2, err := e.svc.ListMovements(e.ctx, f, page.NextCursor)
	e.must(err)
	if len(page2.Data) != 2 || page2.NextCursor != "" || page2.Data[0].ID >= page.Data[3].ID {
		t.Fatalf("page 2 = %+v", page2)
	}
	_, err = e.svc.ListMovements(e.ctx, f, "garbage")
	e.wantCode(err, domain.CodeValidation)

	for _, g := range []domain.StockGrouping{domain.GroupByProduct, domain.GroupByWarehouse, domain.GroupByLocation, domain.GroupByLot} {
		rows, err := e.svc.StockReport(e.ctx, store.StockFilter{ProductID: p.ID, GroupBy: g})
		e.must(err)
		want := map[domain.StockGrouping]int{"product": 1, "warehouse": 1, "location": 3, "lot": 3}[g]
		if len(rows) != want {
			t.Errorf("group_by %s: %d rows, want %d", g, len(rows), want)
		}
	}
}
