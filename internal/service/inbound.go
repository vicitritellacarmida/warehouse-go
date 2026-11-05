package service

import (
	"context"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// Reference types written to the movement ledger and cost tables.
const (
	refReceipt    = "receipt"
	refOrder      = "sales_order"
	refShipment   = "shipment"
	refTransfer   = "transfer"
	refCycleCount = "cycle_count"
	refMove       = "stock_move"
)

// CreateReceiptInput is an expected supplier delivery.
type CreateReceiptInput struct {
	WarehouseID int64               `json:"warehouse_id"`
	Reference   string              `json:"reference"`
	Supplier    string              `json:"supplier"`
	ToleranceBP *int64              `json:"tolerance_bp,omitempty"`
	Lines       []CreateReceiptLine `json:"lines"`
}

// CreateReceiptLine is one expected product. Quantity is in UOM (the base
// unit when empty); UnitCost is always in IDR minor units per base unit,
// so no rounding happens when a line is entered in boxes.
type CreateReceiptLine struct {
	ProductID int64  `json:"product_id"`
	Quantity  int64  `json:"quantity"`
	UOM       string `json:"uom,omitempty"`
	UnitCost  int64  `json:"unit_cost"`
}

// CreateReceipt stores a new open receipt with quantities in base units.
func (s *Service) CreateReceipt(ctx context.Context, in CreateReceiptInput) (*domain.Receipt, error) {
	r := &domain.Receipt{
		WarehouseID: in.WarehouseID, Reference: in.Reference, Supplier: in.Supplier,
		Status: domain.ReceiptOpen, ToleranceBP: s.tolBP, CreatedAt: s.now(),
	}
	if in.ToleranceBP != nil {
		r.ToleranceBP = *in.ToleranceBP
	}
	for _, l := range in.Lines {
		r.Lines = append(r.Lines, domain.ReceiptLine{ProductID: l.ProductID})
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		if _, err := q.GetWarehouse(ctx, in.WarehouseID); err != nil {
			return err
		}
		products, err := q.GetProducts(ctx, productIDs(in.Lines, func(l CreateReceiptLine) int64 { return l.ProductID }))
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			p := products[l.ProductID]
			var err error
			if r.Lines[i].ExpectedQty, err = p.ToBase(l.Quantity, l.UOM); err != nil {
				return err
			}
			if l.UnitCost < 0 {
				return domain.Invalid(domain.CodeValidation, "unit_cost must not be negative")
			}
			r.Lines[i].UnitCost = l.UnitCost
		}
		return q.CreateReceipt(ctx, r)
	})
	if err != nil {
		return nil, err
	}
	return s.GetReceipt(ctx, r.ID)
}

// GetReceipt returns a receipt by id.
func (s *Service) GetReceipt(ctx context.Context, id int64) (*domain.Receipt, error) {
	return s.store.Q().GetReceipt(ctx, id, false)
}

// ReceiveInput lists what physically arrived.
type ReceiveInput struct {
	LocationID int64         `json:"location_id"`
	Lines      []ReceiveLine `json:"lines"`
}

// ReceiveLine is a received quantity of one receipt line, with lot data
// for lot-tracked products.
type ReceiveLine struct {
	LineID     int64        `json:"line_id"`
	Quantity   int64        `json:"quantity"`
	UOM        string       `json:"uom,omitempty"`
	LotNumber  string       `json:"lot_number,omitempty"`
	ExpiryDate *domain.Date `json:"expiry_date,omitempty"`
}

// Receive books received goods into a receiving location. Each line
// creates a receipt movement and a FIFO cost layer. Receiving more than
// expected plus tolerance is rejected.
func (s *Service) Receive(ctx context.Context, receiptID int64, in ReceiveInput) (*domain.Receipt, error) {
	if len(in.Lines) == 0 {
		return nil, domain.Invalid(domain.CodeValidation, "at least one line is required")
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		r, err := q.GetReceipt(ctx, receiptID, true)
		if err != nil {
			return err
		}
		if err := r.Status.CheckTransition(domain.ReceiptReceiving); err != nil {
			return err
		}
		if _, err := requireLocation(ctx, q, in.LocationID, r.WarehouseID, domain.LocationReceiving); err != nil {
			return err
		}
		products, err := q.GetProducts(ctx, productIDs(r.Lines, func(l domain.ReceiptLine) int64 { return l.ProductID }))
		if err != nil {
			return err
		}
		for _, rl := range in.Lines {
			line := r.Line(rl.LineID)
			if line == nil {
				return domain.Invalid(domain.CodeValidation, "receipt %d has no line %d", r.ID, rl.LineID)
			}
			p := products[line.ProductID]
			qty, err := p.ToBase(rl.Quantity, rl.UOM)
			if err != nil {
				return err
			}
			if err := domain.CheckReceive(*line, r.ToleranceBP, qty); err != nil {
				return err
			}
			lotID, err := s.resolveLot(ctx, q, p, rl.LotNumber, rl.ExpiryDate)
			if err != nil {
				return err
			}
			loc := in.LocationID
			if _, err := s.applyMove(ctx, q, move{
				Type: domain.MoveReceipt, ProductID: p.ID, WarehouseID: r.WarehouseID, LotID: lotID,
				To: &loc, Quantity: qty, RefType: refReceipt, RefID: r.ID,
			}); err != nil {
				return err
			}
			if err := s.addCostLayer(ctx, q, p.ID, r.WarehouseID, line.UnitCost, qty, refReceipt, r.ID); err != nil {
				return err
			}
			line.ReceivedQty += qty
			if err := q.SetReceiptLineReceived(ctx, line.ID, line.ReceivedQty); err != nil {
				return err
			}
		}
		return q.SetReceiptStatus(ctx, r.ID, domain.ReceiptReceiving, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.GetReceipt(ctx, receiptID)
}

// CloseReceiptInput controls how a receipt is closed.
type CloseReceiptInput struct {
	// AcceptShort allows closing when some lines are below expected minus
	// tolerance.
	AcceptShort bool `json:"accept_short"`
}

// CloseReceipt closes a receipt. Short lines need AcceptShort.
func (s *Service) CloseReceipt(ctx context.Context, receiptID int64, in CloseReceiptInput) (*domain.Receipt, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		r, err := q.GetReceipt(ctx, receiptID, true)
		if err != nil {
			return err
		}
		if err := r.Status.CheckTransition(domain.ReceiptClosed); err != nil {
			return err
		}
		var short []int64
		for _, l := range r.Lines {
			if l.Outcome != domain.OutcomeComplete {
				short = append(short, l.ID)
			}
		}
		if len(short) > 0 && !in.AcceptShort {
			return domain.Invalid(domain.CodeUnderReceipt,
				"%d line(s) are below the expected quantity minus tolerance; set accept_short to close anyway", len(short)).
				With("line_ids", short)
		}
		now := s.now()
		return q.SetReceiptStatus(ctx, r.ID, domain.ReceiptClosed, &now)
	})
	if err != nil {
		return nil, err
	}
	return s.GetReceipt(ctx, receiptID)
}

// CancelReceipt cancels a receipt on which nothing was received yet.
func (s *Service) CancelReceipt(ctx context.Context, receiptID int64) (*domain.Receipt, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		r, err := q.GetReceipt(ctx, receiptID, true)
		if err != nil {
			return err
		}
		if err := r.Status.CheckTransition(domain.ReceiptCancelled); err != nil {
			return err
		}
		return q.SetReceiptStatus(ctx, r.ID, domain.ReceiptCancelled, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.GetReceipt(ctx, receiptID)
}

// MoveInput moves free (unreserved) stock between two locations of the
// same warehouse.
type MoveInput struct {
	ProductID      int64  `json:"product_id"`
	LotID          *int64 `json:"lot_id,omitempty"`
	FromLocationID int64  `json:"from_location_id"`
	ToLocationID   int64  `json:"to_location_id"`
	Quantity       int64  `json:"quantity"`
	UOM            string `json:"uom,omitempty"`
	Note           string `json:"note,omitempty"`
}

// Putaway moves received stock from a receiving location into storage,
// picking or quarantine.
func (s *Service) Putaway(ctx context.Context, in MoveInput) (*domain.Movement, error) {
	return s.moveStock(ctx, in, true)
}

// MoveStock moves free stock between internal locations. Shipping
// locations can be a source (to return cancelled picks) but not a
// destination; stock reaches shipping only by picking.
func (s *Service) MoveStock(ctx context.Context, in MoveInput) (*domain.Movement, error) {
	return s.moveStock(ctx, in, false)
}

func (s *Service) moveStock(ctx context.Context, in MoveInput, putaway bool) (*domain.Movement, error) {
	var result *domain.Movement
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		from, err := q.GetLocation(ctx, in.FromLocationID)
		if err != nil {
			return err
		}
		mtype := domain.MoveInternal
		if putaway {
			if from.Type != domain.LocationReceiving {
				return domain.Invalid(domain.CodeWrongLocationType, "put-away must start in a receiving location")
			}
			mtype = domain.MovePutaway
		}
		if _, err := requireLocation(ctx, q, in.ToLocationID, from.WarehouseID,
			domain.LocationStorage, domain.LocationPicking, domain.LocationQuarantine, domain.LocationReceiving); err != nil {
			return err
		}
		p, err := q.GetProduct(ctx, in.ProductID)
		if err != nil {
			return err
		}
		if err := checkLotID(ctx, q, p, in.LotID); err != nil {
			return err
		}
		qty, err := p.ToBase(in.Quantity, in.UOM)
		if err != nil {
			return err
		}
		fromID, toID := in.FromLocationID, in.ToLocationID
		res, err := s.applyMove(ctx, q, move{
			Type: mtype, ProductID: p.ID, WarehouseID: from.WarehouseID, LotID: in.LotID,
			From: &fromID, To: &toID, Quantity: qty, RefType: refMove, Note: in.Note,
		})
		if err != nil {
			return err
		}
		result = &res.Movement
		return nil
	})
	return result, err
}
