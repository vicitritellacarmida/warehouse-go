package service

import (
	"context"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// CreateTransferInput is a request to move stock between warehouses.
type CreateTransferInput struct {
	FromWarehouseID int64             `json:"from_warehouse_id"`
	ToWarehouseID   int64             `json:"to_warehouse_id"`
	Lines           []CreateOrderLine `json:"lines"`
}

// CreateTransfer stores a draft transfer.
func (s *Service) CreateTransfer(ctx context.Context, in CreateTransferInput) (*domain.Transfer, error) {
	switch {
	case in.FromWarehouseID <= 0 || in.ToWarehouseID <= 0:
		return nil, domain.Invalid(domain.CodeValidation, "from_warehouse_id and to_warehouse_id are required")
	case in.FromWarehouseID == in.ToWarehouseID:
		return nil, domain.Invalid(domain.CodeValidation, "source and destination warehouse must differ")
	case len(in.Lines) == 0:
		return nil, domain.Invalid(domain.CodeValidation, "a transfer needs at least one line")
	}
	t := &domain.Transfer{
		FromWarehouseID: in.FromWarehouseID, ToWarehouseID: in.ToWarehouseID,
		Status: domain.TransferDraft, CreatedAt: s.now(),
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		for _, id := range []int64{in.FromWarehouseID, in.ToWarehouseID} {
			if _, err := q.GetWarehouse(ctx, id); err != nil {
				return err
			}
		}
		products, err := q.GetProducts(ctx, productIDs(in.Lines, func(l CreateOrderLine) int64 { return l.ProductID }))
		if err != nil {
			return err
		}
		for _, l := range in.Lines {
			qty, err := products[l.ProductID].ToBase(l.Quantity, l.UOM)
			if err != nil {
				return err
			}
			t.Lines = append(t.Lines, domain.TransferLine{ProductID: l.ProductID, Quantity: qty})
		}
		return q.CreateTransfer(ctx, t)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(ctx, t.ID)
}

// GetTransfer returns a transfer by id.
func (s *Service) GetTransfer(ctx context.Context, id int64) (*domain.Transfer, error) {
	return s.store.Q().GetTransfer(ctx, id, false)
}

// ShipTransfer takes the full quantity of every line out of the source
// warehouse (FEFO/FIFO, like order allocation), consumes cost layers and
// puts the transfer in transit. Partial shipment is not allowed: if any
// line is short the call fails with insufficient_stock.
func (s *Service) ShipTransfer(ctx context.Context, id int64) (*domain.Transfer, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		t, err := q.GetTransfer(ctx, id, true)
		if err != nil {
			return err
		}
		if err := t.Status.CheckTransition(domain.TransferInTransit); err != nil {
			return err
		}
		pids := productIDs(t.Lines, func(l domain.TransferLine) int64 { return l.ProductID })
		products, err := q.GetProducts(ctx, pids)
		if err != nil {
			return err
		}
		demands := make([]domain.Demand, len(t.Lines))
		for i, l := range t.Lines {
			demands[i] = domain.Demand{Key: l.ID, ProductID: l.ProductID, Quantity: l.Quantity,
				Strategy: domain.StrategyFor(products[l.ProductID].TrackingMode)}
		}
		stock, err := q.LockAllocatableStock(ctx, t.FromWarehouseID, pids)
		if err != nil {
			return err
		}
		results := domain.AllocateDemands(demands, candidatesByProduct(stock), s.today())
		for _, r := range results {
			if r.Shortage > 0 {
				return domain.Conflict(domain.CodeInsufficientStock, "product %d is short by %d units", r.ProductID, r.Shortage).
					With("product_id", r.ProductID).With("shortage", r.Shortage)
			}
		}
		now := s.now()
		for _, r := range results {
			line := t.Line(r.Key)
			for _, p := range r.Picks {
				from := p.LocationID
				if _, err := s.applyMove(ctx, q, move{
					Type: domain.MoveTransferOut, ProductID: line.ProductID, WarehouseID: t.FromWarehouseID,
					LotID: p.LotID, From: &from, Quantity: p.Quantity, RefType: refTransfer, RefID: t.ID,
				}); err != nil {
					return err
				}
				if err := q.AddTransferLot(ctx, line.ID, p.LotID, p.ReceivedAt, p.Quantity); err != nil {
					return err
				}
			}
			cons, _, err := s.consumeCost(ctx, q, line.ProductID, t.FromWarehouseID, line.Quantity, refTransfer, t.ID)
			if err != nil {
				return err
			}
			for _, c := range cons {
				if err := q.AddTransferCost(ctx, line.ID, c.UnitCost, c.Quantity, now); err != nil {
					return err
				}
			}
			if err := q.SetTransferLineQty(ctx, line.ID, line.Quantity, 0); err != nil {
				return err
			}
		}
		t.Status, t.ShippedAt = domain.TransferInTransit, &now
		return q.SetTransferStatus(ctx, t)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(ctx, id)
}

// ReceiveTransferInput says what arrived at the destination. When Lines
// is empty every line is received in full. Otherwise every line must be
// listed.
type ReceiveTransferInput struct {
	LocationID int64                 `json:"location_id"`
	Lines      []ReceiveTransferLine `json:"lines,omitempty"`
}

// ReceiveTransferLine is the received quantity (base units) of one line.
type ReceiveTransferLine struct {
	LineID   int64 `json:"line_id"`
	Quantity int64 `json:"quantity"`
}

// ReceiveTransfer books the arrived units into a receiving location of
// the destination warehouse, keeping their lots and FIFO dates, and creates
// cost layers at the original unit costs. Units that did not arrive are a
// discrepancy: the transfer ends in received_with_discrepancy and the lost
// value is reported on the line.
func (s *Service) ReceiveTransfer(ctx context.Context, id int64, in ReceiveTransferInput) (*domain.Transfer, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		t, err := q.GetTransfer(ctx, id, true)
		if err != nil {
			return err
		}
		if err := t.Status.CheckTransition(domain.TransferReceived); err != nil {
			return err
		}
		if _, err := requireLocation(ctx, q, in.LocationID, t.ToWarehouseID, domain.LocationReceiving); err != nil {
			return err
		}
		received := map[int64]int64{}
		if len(in.Lines) == 0 {
			for _, l := range t.Lines {
				received[l.ID] = l.QtyShipped
			}
		}
		for _, rl := range in.Lines {
			l := t.Line(rl.LineID)
			switch {
			case l == nil:
				return domain.Invalid(domain.CodeValidation, "transfer %d has no line %d", t.ID, rl.LineID)
			case rl.Quantity < 0:
				return domain.Invalid(domain.CodeValidation, "received quantity must not be negative")
			case rl.Quantity > l.QtyShipped:
				return domain.Invalid(domain.CodeTransferOverReceipt, "line %d shipped %d units, %d received",
					l.ID, l.QtyShipped, rl.Quantity).With("line_id", l.ID)
			}
			if _, dup := received[l.ID]; dup {
				return domain.Invalid(domain.CodeValidation, "line %d is listed twice", l.ID)
			}
			received[l.ID] = rl.Quantity
		}
		if len(received) != len(t.Lines) {
			return domain.Invalid(domain.CodeValidation, "every transfer line must be listed when lines are given")
		}

		status := domain.TransferReceived
		for i := range t.Lines {
			l := &t.Lines[i]
			rec := received[l.ID]
			if rec < l.QtyShipped {
				status = domain.TransferDiscrepancy
			}
			for j, qty := range domain.Distribute(l.Lots, rec) {
				part := l.Lots[j]
				if qty > 0 {
					to := in.LocationID
					if _, err := s.applyMove(ctx, q, move{
						Type: domain.MoveTransferIn, ProductID: l.ProductID, WarehouseID: t.ToWarehouseID,
						LotID: part.LotID, To: &to, Quantity: qty, ReceivedAt: part.ReceivedAt,
						RefType: refTransfer, RefID: t.ID,
					}); err != nil {
						return err
					}
				}
				if err := q.SetTransferPartReceived(ctx, part.ID, qty); err != nil {
					return err
				}
			}
			for j, qty := range domain.Distribute(l.Costs, rec) {
				part := l.Costs[j]
				if qty > 0 {
					if err := s.addCostLayer(ctx, q, l.ProductID, t.ToWarehouseID, part.UnitCost, qty, refTransfer, t.ID); err != nil {
						return err
					}
				}
				if err := q.SetTransferPartReceived(ctx, part.ID, qty); err != nil {
					return err
				}
			}
			if err := q.SetTransferLineQty(ctx, l.ID, l.QtyShipped, rec); err != nil {
				return err
			}
		}
		now := s.now()
		t.Status, t.ReceivedAt = status, &now
		return q.SetTransferStatus(ctx, t)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(ctx, id)
}

// CancelTransfer cancels a draft transfer.
func (s *Service) CancelTransfer(ctx context.Context, id int64) (*domain.Transfer, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		t, err := q.GetTransfer(ctx, id, true)
		if err != nil {
			return err
		}
		if err := t.Status.CheckTransition(domain.TransferCancelled); err != nil {
			return err
		}
		t.Status = domain.TransferCancelled
		return q.SetTransferStatus(ctx, t)
	})
	if err != nil {
		return nil, err
	}
	return s.GetTransfer(ctx, id)
}
