package service

import (
	"context"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// move describes one stock movement. It is the only way stock rows change
// on hand, so the ledger and the stock rows always agree.
type move struct {
	Type        domain.MovementType
	ProductID   int64
	WarehouseID int64
	LotID       *int64
	From, To    *int64 // location ids; at least one is set
	Quantity    int64
	// Reserved means the units are reserved at the source and stay
	// reserved at the destination (picking, shipping).
	Reserved bool
	// ReceivedAt is the FIFO date of the moved units. When zero it is taken
	// from the source row, or the current time when there is no source.
	ReceivedAt time.Time
	RefType    string
	RefID      int64
	Note       string
	// IgnoreCount lets a cycle count post adjustments into its own
	// blocked location.
	IgnoreCount bool
}

// moveResult is what applyMove wrote.
type moveResult struct {
	ToItemID int64 // destination stock row, 0 when there is no destination
	Movement domain.Movement
}

// applyMove locks the involved locations, checks cycle-count blocks and
// capacity, updates the stock rows and appends the ledger entry.
func (s *Service) applyMove(ctx context.Context, q *store.Queries, m move) (moveResult, error) {
	if m.Quantity <= 0 {
		return moveResult{}, domain.Invalid(domain.CodeValidation, "movement quantity must be positive")
	}
	if m.From != nil && m.To != nil && *m.From == *m.To {
		return moveResult{}, domain.Invalid(domain.CodeValidation, "source and destination location are the same")
	}
	now := s.now()

	var ids []int64
	exclusive := map[int64]bool{}
	if m.From != nil {
		ids = append(ids, *m.From)
	}
	if m.To != nil {
		ids = append(ids, *m.To)
		// Capacity checks read the location total, so the destination is
		// locked exclusively when it has a capacity.
		dest, err := q.GetLocation(ctx, *m.To)
		if err != nil {
			return moveResult{}, err
		}
		exclusive[*m.To] = dest.Capacity != nil
	}
	locs, err := q.LockLocations(ctx, ids, exclusive)
	if err != nil {
		return moveResult{}, err
	}
	for _, l := range locs {
		if l.WarehouseID != m.WarehouseID {
			return moveResult{}, domain.Invalid(domain.CodeWrongWarehouse, "location %s is not in warehouse %d", l.Code, m.WarehouseID)
		}
		if !m.IgnoreCount {
			if err := l.CheckNotCounting(); err != nil {
				return moveResult{}, err
			}
		}
	}

	receivedAt := m.ReceivedAt
	if m.From != nil {
		src, err := q.FindStockItemForUpdate(ctx, m.ProductID, *m.From, m.LotID)
		if err != nil {
			return moveResult{}, err
		}
		free := int64(0)
		if src != nil {
			free = src.OnHand - src.Reserved
			if m.Reserved {
				free = src.Reserved
			}
		}
		if free < m.Quantity {
			return moveResult{}, domain.Conflict(domain.CodeInsufficientStock, "location %s holds %d usable units, %d requested",
				locs[*m.From].Code, free, m.Quantity).
				With("location_id", *m.From).With("product_id", m.ProductID).With("available", free)
		}
		dReserved := int64(0)
		if m.Reserved {
			dReserved = -m.Quantity
		}
		if err := q.UpdateStock(ctx, src.ID, -m.Quantity, dReserved, now); err != nil {
			return moveResult{}, err
		}
		if receivedAt.IsZero() {
			receivedAt = src.ReceivedAt
		}
	}

	var toItem int64
	if m.To != nil {
		dest := locs[*m.To]
		if dest.Capacity != nil {
			current, err := q.LocationOnHand(ctx, dest.ID)
			if err != nil {
				return moveResult{}, err
			}
			if err := dest.CheckCapacity(current, m.Quantity); err != nil {
				return moveResult{}, err
			}
		}
		if receivedAt.IsZero() {
			receivedAt = now
		}
		if toItem, err = q.EnsureStockItem(ctx, m.ProductID, m.WarehouseID, dest.ID, m.LotID, receivedAt); err != nil {
			return moveResult{}, err
		}
		dReserved := int64(0)
		if m.Reserved {
			dReserved = m.Quantity
		}
		if err := q.UpdateStock(ctx, toItem, m.Quantity, dReserved, now); err != nil {
			return moveResult{}, err
		}
	}

	mv := domain.Movement{
		Type:           m.Type,
		ProductID:      m.ProductID,
		WarehouseID:    m.WarehouseID,
		LotID:          m.LotID,
		FromLocationID: m.From,
		ToLocationID:   m.To,
		Quantity:       m.Quantity,
		ReferenceType:  m.RefType,
		ReferenceID:    m.RefID,
		Note:           m.Note,
		CreatedAt:      now,
	}
	if err := q.InsertMovement(ctx, &mv); err != nil {
		return moveResult{}, err
	}
	return moveResult{ToItemID: toItem, Movement: mv}, nil
}

// addCostLayer records units entering a warehouse at a unit cost.
func (s *Service) addCostLayer(ctx context.Context, q *store.Queries, productID, warehouseID, unitCost, qty int64,
	sourceType string, sourceID int64) error {
	return q.InsertCostLayer(ctx, &domain.CostLayer{
		ProductID:    productID,
		WarehouseID:  warehouseID,
		UnitCost:     unitCost,
		QtyReceived:  qty,
		QtyRemaining: qty,
		SourceType:   sourceType,
		SourceID:     sourceID,
		ReceivedAt:   s.now(),
	})
}

// consumeCost takes qty units from the product's cost layers in the
// warehouse, oldest first, and returns the consumptions and total cost.
func (s *Service) consumeCost(ctx context.Context, q *store.Queries, productID, warehouseID, qty int64,
	refType string, refID int64) ([]domain.Consumption, int64, error) {
	layers, err := q.LockOpenCostLayers(ctx, productID, warehouseID)
	if err != nil {
		return nil, 0, err
	}
	cons, total, err := domain.ConsumeFIFO(layers, qty)
	if err != nil {
		return nil, 0, err
	}
	now := s.now()
	for _, c := range cons {
		if err := q.ConsumeCostLayer(ctx, c, refType, refID, now); err != nil {
			return nil, 0, err
		}
	}
	return cons, total, nil
}

// resolveLot validates lot input against the product and returns the lot
// id, creating the lot when it is new. It returns nil for untracked
// products.
func (s *Service) resolveLot(ctx context.Context, q *store.Queries, p *domain.Product, lotNumber string, expiry *domain.Date) (*int64, error) {
	if err := domain.ValidateLotInput(p, lotNumber, expiry); err != nil {
		return nil, err
	}
	if !p.TrackingMode.RequiresLot() {
		return nil, nil
	}
	lot, err := q.GetOrCreateLot(ctx, p.ID, lotNumber, expiry, s.now())
	if err != nil {
		return nil, err
	}
	return &lot.ID, nil
}

// checkLotID validates an existing lot id against the product.
func checkLotID(ctx context.Context, q *store.Queries, p *domain.Product, lotID *int64) error {
	if !p.TrackingMode.RequiresLot() {
		if lotID != nil {
			return domain.Invalid(domain.CodeLotNotAllowed, "product %s is not lot tracked", p.SKU)
		}
		return nil
	}
	if lotID == nil {
		return domain.Invalid(domain.CodeLotRequired, "product %s is lot tracked; lot_id is required", p.SKU)
	}
	lot, err := q.GetLot(ctx, *lotID)
	if err != nil {
		return err
	}
	if lot.ProductID != p.ID {
		return domain.Invalid(domain.CodeValidation, "lot %d does not belong to product %s", *lotID, p.SKU)
	}
	return nil
}
