package domain

import (
	"slices"
	"time"
)

// Strategy is the order in which stock is consumed when allocating.
type Strategy string

// Allocation strategies.
const (
	// FEFO is "first expiry, first out": the lot that expires soonest is
	// used first. Expired lots are skipped.
	FEFO Strategy = "fefo"
	// FIFO is "first in, first out": the oldest received stock is used first.
	FIFO Strategy = "fifo"
)

// StrategyFor returns FEFO for expiry-tracked products and FIFO otherwise.
func StrategyFor(m TrackingMode) Strategy {
	if m == TrackingLotExpiry {
		return FEFO
	}
	return FIFO
}

// Candidate is a stock row that could supply an allocation.
type Candidate struct {
	StockItemID int64
	LocationID  int64
	LotID       *int64
	Expiry      *Date
	ReceivedAt  time.Time
	Available   int64
}

// Pick is a planned quantity taken from one stock row.
type Pick struct {
	StockItemID int64
	LocationID  int64
	LotID       *int64
	ReceivedAt  time.Time
	Quantity    int64
}

// Demand is a quantity of one product needed by one document line.
type Demand struct {
	Key       int64 // caller's line identifier
	ProductID int64
	Quantity  int64
	Strategy  Strategy
}

// DemandResult is the outcome of allocating one Demand.
type DemandResult struct {
	Key       int64
	ProductID int64
	Picks     []Pick
	Allocated int64
	Shortage  int64
}

// SortCandidates orders candidates by the given strategy. The order is fully
// deterministic: ties are broken by receipt time and then stock row id.
// For FEFO, rows without an expiry date come after rows with one.
func SortCandidates(c []Candidate, s Strategy) {
	slices.SortStableFunc(c, func(a, b Candidate) int {
		if s == FEFO {
			switch {
			case a.Expiry != nil && b.Expiry != nil && !a.Expiry.Equal(*b.Expiry):
				return a.Expiry.Time().Compare(b.Expiry.Time())
			case a.Expiry != nil && b.Expiry == nil:
				return -1
			case a.Expiry == nil && b.Expiry != nil:
				return 1
			}
		}
		if c := a.ReceivedAt.Compare(b.ReceivedAt); c != 0 {
			return c
		}
		switch {
		case a.StockItemID < b.StockItemID:
			return -1
		case a.StockItemID > b.StockItemID:
			return 1
		}
		return 0
	})
}

// Allocate plans picks for qty units from candidates. It never takes more
// than a candidate's Available quantity and, for FEFO, skips lots that
// expired before today. It returns the picks and the quantity it could not
// cover (the shortage). The input slice is not modified.
func Allocate(qty int64, candidates []Candidate, s Strategy, today Date) ([]Pick, int64) {
	sorted := slices.Clone(candidates)
	SortCandidates(sorted, s)
	remaining := qty
	var picks []Pick
	for _, c := range sorted {
		if remaining <= 0 {
			break
		}
		if c.Available <= 0 || (s == FEFO && IsExpired(c.Expiry, today)) {
			continue
		}
		take := min(remaining, c.Available)
		picks = append(picks, Pick{
			StockItemID: c.StockItemID,
			LocationID:  c.LocationID,
			LotID:       c.LotID,
			ReceivedAt:  c.ReceivedAt,
			Quantity:    take,
		})
		remaining -= take
	}
	return picks, remaining
}

// IsExpired reports whether a lot with the given expiry date can no longer
// be shipped on today. Stock that expires today is still usable.
func IsExpired(expiry *Date, today Date) bool {
	return expiry != nil && expiry.Before(today)
}

// AllocateDemands allocates several demands against a shared pool of
// candidates, keyed by product id. Demands are served in the order given,
// so two lines for the same product never take the same units.
func AllocateDemands(demands []Demand, pool map[int64][]Candidate, today Date) []DemandResult {
	remaining := make(map[int64][]Candidate, len(pool))
	for pid, cands := range pool {
		remaining[pid] = slices.Clone(cands)
	}
	results := make([]DemandResult, 0, len(demands))
	for _, d := range demands {
		cands := remaining[d.ProductID]
		picks, short := Allocate(d.Quantity, cands, d.Strategy, today)
		used := make(map[int64]int64, len(picks))
		for _, p := range picks {
			used[p.StockItemID] += p.Quantity
		}
		for i := range cands {
			cands[i].Available -= used[cands[i].StockItemID]
		}
		results = append(results, DemandResult{
			Key:       d.Key,
			ProductID: d.ProductID,
			Picks:     picks,
			Allocated: d.Quantity - short,
			Shortage:  short,
		})
	}
	return results
}
