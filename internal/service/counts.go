package service

import (
	"context"
	"strings"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// OpenCountInput names the location to count.
type OpenCountInput struct {
	LocationID int64 `json:"location_id"`
}

// OpenCount starts a cycle count: it snapshots the system quantity of every
// stock row in the location and blocks the location for movements until
// the count is posted or cancelled.
func (s *Service) OpenCount(ctx context.Context, in OpenCountInput) (*domain.CycleCount, error) {
	var countID int64
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		locs, err := q.LockLocations(ctx, []int64{in.LocationID}, map[int64]bool{in.LocationID: true})
		if err != nil {
			return err
		}
		loc := locs[in.LocationID]
		if loc.OpenCountID != nil {
			return domain.Conflict(domain.CodeCountAlreadyOpen, "location %s already has open count %d", loc.Code, *loc.OpenCountID).
				With("cycle_count_id", *loc.OpenCountID)
		}
		items, err := q.StockAtLocation(ctx, loc.ID)
		if err != nil {
			return err
		}
		c := &domain.CycleCount{LocationID: loc.ID, WarehouseID: loc.WarehouseID, Status: domain.CountOpen, CreatedAt: s.now()}
		for _, it := range items {
			c.Lines = append(c.Lines, domain.CountLine{ProductID: it.ProductID, LotID: it.LotID, SystemQty: it.OnHand})
		}
		if err := q.CreateCount(ctx, c); err != nil {
			return err
		}
		countID = c.ID
		return q.SetLocationCount(ctx, loc.ID, &c.ID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCount(ctx, countID)
}

// GetCount returns a cycle count by id.
func (s *Service) GetCount(ctx context.Context, id int64) (*domain.CycleCount, error) {
	return s.store.Q().GetCount(ctx, id, false)
}

// RecordCountInput carries counted quantities.
type RecordCountInput struct {
	Lines []RecordCountLine `json:"lines"`
}

// RecordCountLine is the counted quantity of one product and lot. A lot
// that the system did not know about in this location can be reported by
// lot number (and expiry date for expiry-tracked products).
type RecordCountLine struct {
	ProductID  int64        `json:"product_id"`
	LotNumber  string       `json:"lot_number,omitempty"`
	ExpiryDate *domain.Date `json:"expiry_date,omitempty"`
	Counted    int64        `json:"counted_qty"`
	UOM        string       `json:"uom,omitempty"`
}

// RecordCount stores counted quantities on an open count. Recording the
// same product and lot again overwrites the earlier value.
func (s *Service) RecordCount(ctx context.Context, id int64, in RecordCountInput) (*domain.CycleCount, error) {
	if len(in.Lines) == 0 {
		return nil, domain.Invalid(domain.CodeValidation, "at least one line is required")
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		c, err := q.GetCount(ctx, id, true)
		if err != nil {
			return err
		}
		if c.Status != domain.CountOpen {
			return domain.Conflict(domain.CodeInvalidTransition, "cycle count %d is %s", c.ID, c.Status)
		}
		products, err := q.GetProducts(ctx, productIDs(in.Lines, func(l RecordCountLine) int64 { return l.ProductID }))
		if err != nil {
			return err
		}
		for _, rl := range in.Lines {
			p := products[rl.ProductID]
			counted := rl.Counted
			if counted < 0 {
				return domain.Invalid(domain.CodeValidation, "counted_qty must not be negative")
			}
			if counted > 0 {
				if counted, err = p.ToBase(counted, rl.UOM); err != nil {
					return err
				}
			}
			lotID, err := s.resolveLot(ctx, q, p, rl.LotNumber, rl.ExpiryDate)
			if err != nil {
				return err
			}
			line := domain.CountLine{ProductID: p.ID, LotID: lotID, CountedQty: &counted}
			if err := q.UpsertCountLine(ctx, c.ID, &line); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetCount(ctx, id)
}

// PostCountInput gives the reason for the adjustments.
type PostCountInput struct {
	Reason domain.AdjustmentReason `json:"reason"`
	Note   string                  `json:"note,omitempty"`
}

// PostCount compares counted with system quantities, writes an adjustment
// movement for every variance and unblocks the location. Gains create a
// cost layer at the latest known unit cost (0 if none); losses consume
// FIFO layers. Every line must be counted first.
func (s *Service) PostCount(ctx context.Context, id int64, in PostCountInput) (*domain.CycleCount, error) {
	if !in.Reason.Valid() {
		return nil, domain.Invalid(domain.CodeValidation,
			"reason must be one of damaged, lost, found, miscount, expired, other")
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 200 {
		return nil, domain.Invalid(domain.CodeValidation, "note must be at most 200 characters")
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		c, err := q.GetCount(ctx, id, true)
		if err != nil {
			return err
		}
		if err := c.Status.CheckTransition(domain.CountPosted); err != nil {
			return err
		}
		var uncounted []int64
		for _, l := range c.Lines {
			if l.CountedQty == nil {
				uncounted = append(uncounted, l.ID)
			}
		}
		if len(uncounted) > 0 {
			return domain.Invalid(domain.CodeUncountedLines, "%d line(s) have no counted quantity", len(uncounted)).
				With("line_ids", uncounted)
		}
		if _, err := q.LockLocations(ctx, []int64{c.LocationID}, map[int64]bool{c.LocationID: true}); err != nil {
			return err
		}
		note := string(in.Reason)
		if in.Note != "" {
			note += ": " + in.Note
		}
		for _, l := range c.Lines {
			current, err := q.FindStockItemForUpdate(ctx, l.ProductID, c.LocationID, l.LotID)
			if err != nil {
				return err
			}
			var onHand int64
			if current != nil {
				onHand = current.OnHand
			}
			variance := *l.CountedQty - onHand
			loc := c.LocationID
			m := move{
				Type: domain.MoveAdjustment, ProductID: l.ProductID, WarehouseID: c.WarehouseID, LotID: l.LotID,
				RefType: refCycleCount, RefID: c.ID, Note: note, IgnoreCount: true,
			}
			switch {
			case variance > 0:
				m.To, m.Quantity = &loc, variance
				if _, err := s.applyMove(ctx, q, m); err != nil {
					return err
				}
				cost, _, err := q.LatestUnitCost(ctx, l.ProductID, c.WarehouseID)
				if err != nil {
					return err
				}
				if err := s.addCostLayer(ctx, q, l.ProductID, c.WarehouseID, cost, variance, refCycleCount, c.ID); err != nil {
					return err
				}
			case variance < 0:
				m.From, m.Quantity = &loc, -variance
				if _, err := s.applyMove(ctx, q, m); err != nil {
					return err
				}
				if _, _, err := s.consumeCost(ctx, q, l.ProductID, c.WarehouseID, -variance, refCycleCount, c.ID); err != nil {
					return err
				}
			}
		}
		c.Status, c.Reason, c.Note = domain.CountPosted, in.Reason, in.Note
		if err := q.FinishCount(ctx, c, s.now()); err != nil {
			return err
		}
		return q.SetLocationCount(ctx, c.LocationID, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCount(ctx, id)
}

// CancelCount abandons an open count and unblocks the location.
func (s *Service) CancelCount(ctx context.Context, id int64) (*domain.CycleCount, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		c, err := q.GetCount(ctx, id, true)
		if err != nil {
			return err
		}
		if err := c.Status.CheckTransition(domain.CountCancelled); err != nil {
			return err
		}
		if _, err := q.LockLocations(ctx, []int64{c.LocationID}, map[int64]bool{c.LocationID: true}); err != nil {
			return err
		}
		c.Status = domain.CountCancelled
		if err := q.FinishCount(ctx, c, s.now()); err != nil {
			return err
		}
		return q.SetLocationCount(ctx, c.LocationID, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCount(ctx, id)
}
