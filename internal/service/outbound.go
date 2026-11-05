package service

import (
	"context"
	"fmt"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// CreateOrderInput is a new sales order.
type CreateOrderInput struct {
	OrderNumber string            `json:"order_number"`
	WarehouseID int64             `json:"warehouse_id"`
	Customer    string            `json:"customer"`
	Lines       []CreateOrderLine `json:"lines"`
}

// CreateOrderLine is one requested product.
type CreateOrderLine struct {
	ProductID int64  `json:"product_id"`
	Quantity  int64  `json:"quantity"`
	UOM       string `json:"uom,omitempty"`
}

// OrderView is an order with its current allocations and shipment.
type OrderView struct {
	*domain.SalesOrder
	Allocations []domain.Allocation `json:"allocations"`
	Shipment    *domain.Shipment    `json:"shipment,omitempty"`
	// BackorderID is set in the response of an allocation that created a
	// backorder.
	BackorderID *int64 `json:"backorder_id,omitempty"`
}

// CreateOrder stores a new order in status new.
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (*OrderView, error) {
	now := s.now()
	o := &domain.SalesOrder{
		OrderNumber: in.OrderNumber, WarehouseID: in.WarehouseID, Customer: in.Customer,
		Status: domain.OrderNew, CreatedAt: now, UpdatedAt: now,
	}
	for _, l := range in.Lines {
		o.Lines = append(o.Lines, domain.OrderLine{ProductID: l.ProductID, Status: domain.LineOpen})
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		if _, err := q.GetWarehouse(ctx, in.WarehouseID); err != nil {
			return err
		}
		products, err := q.GetProducts(ctx, productIDs(in.Lines, func(l CreateOrderLine) int64 { return l.ProductID }))
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			if o.Lines[i].Quantity, err = products[l.ProductID].ToBase(l.Quantity, l.UOM); err != nil {
				return err
			}
		}
		return q.CreateOrder(ctx, o)
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, o.ID)
}

// GetOrder returns an order with allocations and shipment.
func (s *Service) GetOrder(ctx context.Context, id int64) (*OrderView, error) {
	q := s.store.Q()
	o, err := q.GetOrder(ctx, id, false)
	if err != nil {
		return nil, err
	}
	allocs, err := q.ListAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	ship, err := q.GetShipmentByOrder(ctx, id)
	if err != nil {
		return nil, err
	}
	return &OrderView{SalesOrder: o, Allocations: allocs, Shipment: ship}, nil
}

// PickList returns the pick instructions of an order.
func (s *Service) PickList(ctx context.Context, id int64) ([]domain.PickTask, error) {
	q := s.store.Q()
	if _, err := q.GetOrder(ctx, id, false); err != nil {
		return nil, err
	}
	return q.PickList(ctx, id)
}

// AllocateOrder reserves stock for a new order. Each line is filled using
// FEFO for expiry-tracked products and FIFO otherwise, from storage and
// picking locations only. If some quantity cannot be covered, it is moved
// to a new backorder order and the original order continues with what was
// allocated. If nothing at all can be allocated the call fails with
// insufficient_stock and the order stays new.
func (s *Service) AllocateOrder(ctx context.Context, orderID int64) (*OrderView, error) {
	var backorderID *int64
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		backorderID = nil
		o, err := q.GetOrder(ctx, orderID, true)
		if err != nil {
			return err
		}
		if err := o.Status.CheckTransition(domain.OrderAllocated); err != nil {
			return err
		}
		pids := productIDs(o.Lines, func(l domain.OrderLine) int64 { return l.ProductID })
		products, err := q.GetProducts(ctx, pids)
		if err != nil {
			return err
		}
		var demands []domain.Demand
		for _, l := range o.Lines {
			if l.Status != domain.LineOpen {
				continue
			}
			demands = append(demands, domain.Demand{
				Key: l.ID, ProductID: l.ProductID, Quantity: l.Quantity - l.QtyAllocated - l.QtyBackordered,
				Strategy: domain.StrategyFor(products[l.ProductID].TrackingMode),
			})
		}
		stock, err := q.LockAllocatableStock(ctx, o.WarehouseID, pids)
		if err != nil {
			return err
		}
		results := domain.AllocateDemands(demands, candidatesByProduct(stock), s.today())

		var total int64
		for _, r := range results {
			total += r.Allocated
		}
		if total == 0 {
			return domain.Conflict(domain.CodeInsufficientStock, "no stock can be allocated for order %s", o.OrderNumber).
				With("order_id", o.ID)
		}

		now := s.now()
		var backorderLines []domain.OrderLine
		for _, r := range results {
			line := o.Line(r.Key)
			for _, p := range r.Picks {
				if err := q.UpdateStock(ctx, p.StockItemID, 0, p.Quantity, now); err != nil {
					return err
				}
				if err := q.InsertAllocation(ctx, &domain.Allocation{
					OrderID: o.ID, OrderLineID: line.ID, ProductID: line.ProductID, StockItemID: p.StockItemID,
					LocationID: p.LocationID, LotID: p.LotID, Quantity: p.Quantity, Status: domain.AllocReserved,
				}, now); err != nil {
					return err
				}
			}
			line.QtyAllocated += r.Allocated
			line.QtyBackordered += r.Shortage
			line.Status = domain.LineAllocated
			if line.QtyAllocated == 0 {
				line.Status = domain.LineBackordered
			}
			if err := q.UpdateOrderLine(ctx, line); err != nil {
				return err
			}
			if r.Shortage > 0 {
				backorderLines = append(backorderLines, domain.OrderLine{
					ProductID: line.ProductID, Quantity: r.Shortage, Status: domain.LineOpen,
				})
			}
		}
		if len(backorderLines) > 0 {
			bo := &domain.SalesOrder{
				OrderNumber: backorderNumber(o.OrderNumber), WarehouseID: o.WarehouseID, Customer: o.Customer,
				Status: domain.OrderNew, BackorderOfID: &o.ID, Lines: backorderLines, CreatedAt: now, UpdatedAt: now,
			}
			if err := q.CreateOrder(ctx, bo); err != nil {
				return err
			}
			backorderID = &bo.ID
		}
		return q.UpdateOrderStatus(ctx, o.ID, domain.OrderAllocated, now)
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	v.BackorderID = backorderID
	return v, nil
}

// backorderNumber derives the order number of a backorder. Each order is
// allocated at most once, so the suffix is unique.
func backorderNumber(n string) string { return fmt.Sprintf("%s-BO", n) }

func candidatesByProduct(stock []*domain.StockItem) map[int64][]domain.Candidate {
	out := map[int64][]domain.Candidate{}
	for _, si := range stock {
		out[si.ProductID] = append(out[si.ProductID], domain.Candidate{
			StockItemID: si.ID, LocationID: si.LocationID, LotID: si.LotID, Expiry: si.ExpiryDate,
			ReceivedAt: si.ReceivedAt, Available: si.Available(),
		})
	}
	return out
}

// PickInput names the shipping location where picked goods are staged.
type PickInput struct {
	ShippingLocationID int64 `json:"shipping_location_id"`
}

// PickOrder moves every reserved allocation of the order from its bin to a
// shipping location. The units stay reserved for the order.
func (s *Service) PickOrder(ctx context.Context, orderID int64, in PickInput) (*OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.GetOrder(ctx, orderID, true)
		if err != nil {
			return err
		}
		if err := o.Status.CheckTransition(domain.OrderPicked); err != nil {
			return err
		}
		if _, err := requireLocation(ctx, q, in.ShippingLocationID, o.WarehouseID, domain.LocationShipping); err != nil {
			return err
		}
		allocs, err := q.ListAllocations(ctx, o.ID, domain.AllocReserved)
		if err != nil {
			return err
		}
		now := s.now()
		for i := range allocs {
			a := &allocs[i]
			from, to := a.LocationID, in.ShippingLocationID
			res, err := s.applyMove(ctx, q, move{
				Type: domain.MovePick, ProductID: a.ProductID, WarehouseID: o.WarehouseID, LotID: a.LotID,
				From: &from, To: &to, Quantity: a.Quantity, Reserved: true, RefType: refOrder, RefID: o.ID,
			})
			if err != nil {
				return err
			}
			a.StockItemID, a.LocationID, a.Status = res.ToItemID, to, domain.AllocPicked
			if err := q.UpdateAllocation(ctx, a, now); err != nil {
				return err
			}
			o.Line(a.OrderLineID).QtyPicked += a.Quantity
		}
		for i := range o.Lines {
			if err := s.advanceLine(ctx, q, &o.Lines[i], domain.LineAllocated, domain.LinePicked); err != nil {
				return err
			}
		}
		return q.UpdateOrderStatus(ctx, o.ID, domain.OrderPicked, now)
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, orderID)
}

// advanceLine moves a line from one status to the next if it is in from.
func (s *Service) advanceLine(ctx context.Context, q *store.Queries, l *domain.OrderLine, from, to domain.LineStatus) error {
	if l.Status != from {
		return nil
	}
	if err := l.Status.CheckTransition(to); err != nil {
		return err
	}
	l.Status = to
	return q.UpdateOrderLine(ctx, l)
}

// PackOrder marks a picked order as packed.
func (s *Service) PackOrder(ctx context.Context, orderID int64) (*OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.GetOrder(ctx, orderID, true)
		if err != nil {
			return err
		}
		if err := o.Status.CheckTransition(domain.OrderPacked); err != nil {
			return err
		}
		return q.UpdateOrderStatus(ctx, o.ID, domain.OrderPacked, s.now())
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, orderID)
}

// ShipOrder ships a packed order: the staged units leave the shipping
// location, FIFO cost layers are consumed and each shipment line records
// its cost of goods sold.
func (s *Service) ShipOrder(ctx context.Context, orderID int64) (*OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.GetOrder(ctx, orderID, true)
		if err != nil {
			return err
		}
		if err := o.Status.CheckTransition(domain.OrderShipped); err != nil {
			return err
		}
		allocs, err := q.ListAllocations(ctx, o.ID, domain.AllocPicked)
		if err != nil {
			return err
		}
		now := s.now()
		ship := &domain.Shipment{OrderID: o.ID, ShippedAt: now}
		if err := q.CreateShipment(ctx, ship); err != nil {
			return err
		}
		for i := range allocs {
			a := &allocs[i]
			from := a.LocationID
			if _, err := s.applyMove(ctx, q, move{
				Type: domain.MoveShip, ProductID: a.ProductID, WarehouseID: o.WarehouseID, LotID: a.LotID,
				From: &from, Quantity: a.Quantity, Reserved: true, RefType: refShipment, RefID: ship.ID,
			}); err != nil {
				return err
			}
			_, cogs, err := s.consumeCost(ctx, q, a.ProductID, o.WarehouseID, a.Quantity, refShipment, ship.ID)
			if err != nil {
				return err
			}
			line := &domain.ShipmentLine{
				OrderLineID: a.OrderLineID, ProductID: a.ProductID, LotID: a.LotID, Quantity: a.Quantity, COGS: cogs,
			}
			if err := q.AddShipmentLine(ctx, ship.ID, line); err != nil {
				return err
			}
			ship.TotalCOGS += cogs
			a.Status = domain.AllocShipped
			if err := q.UpdateAllocation(ctx, a, now); err != nil {
				return err
			}
			o.Line(a.OrderLineID).QtyShipped += a.Quantity
		}
		if err := q.SetShipmentCOGS(ctx, ship.ID, ship.TotalCOGS); err != nil {
			return err
		}
		for i := range o.Lines {
			if err := s.advanceLine(ctx, q, &o.Lines[i], domain.LinePicked, domain.LineShipped); err != nil {
				return err
			}
		}
		return q.UpdateOrderStatus(ctx, o.ID, domain.OrderShipped, now)
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, orderID)
}

// CancelOrder cancels an order that has not shipped and releases all its
// reservations. Units that were already picked stay in the shipping
// location as free stock and can be moved back with a stock move.
func (s *Service) CancelOrder(ctx context.Context, orderID int64) (*OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.GetOrder(ctx, orderID, true)
		if err != nil {
			return err
		}
		if err := o.Status.CheckTransition(domain.OrderCancelled); err != nil {
			return err
		}
		allocs, err := q.ListAllocations(ctx, o.ID, domain.AllocReserved, domain.AllocPicked)
		if err != nil {
			return err
		}
		ids := make([]int64, len(allocs))
		for i, a := range allocs {
			ids[i] = a.StockItemID
		}
		if _, err := q.LockStockItems(ctx, ids); err != nil {
			return err
		}
		now := s.now()
		for i := range allocs {
			a := &allocs[i]
			if err := q.UpdateStock(ctx, a.StockItemID, 0, -a.Quantity, now); err != nil {
				return err
			}
			a.Status = domain.AllocReleased
			if err := q.UpdateAllocation(ctx, a, now); err != nil {
				return err
			}
		}
		for i := range o.Lines {
			l := &o.Lines[i]
			if l.Status.CanTransition(domain.LineCancelled) {
				l.Status = domain.LineCancelled
				if err := q.UpdateOrderLine(ctx, l); err != nil {
					return err
				}
			}
		}
		return q.UpdateOrderStatus(ctx, o.ID, domain.OrderCancelled, now)
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(ctx, orderID)
}
