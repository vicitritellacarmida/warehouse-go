package domain

import (
	"reflect"
	"testing"
	"time"
)

func dp(y int, m time.Month, d int) *Date { v := NewDate(y, m, d); return &v }
func ip(v int64) *int64                   { return &v }

var t0 = time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

// picked reduces picks to "stock item id -> quantity" pairs for easy
// comparison.
func picked(ps []Pick) [][2]int64 {
	out := [][2]int64{}
	for _, p := range ps {
		out = append(out, [2]int64{p.StockItemID, p.Quantity})
	}
	return out
}

func TestAllocate(t *testing.T) {
	today := NewDate(2026, 6, 1)
	tests := []struct {
		name      string
		strategy  Strategy
		qty       int64
		cands     []Candidate
		wantPicks [][2]int64
		wantShort int64
	}{
		{
			name:     "FIFO takes oldest receipt first",
			strategy: FIFO, qty: 15,
			cands: []Candidate{
				{StockItemID: 1, ReceivedAt: day(5), Available: 10},
				{StockItemID: 2, ReceivedAt: day(1), Available: 10},
				{StockItemID: 3, ReceivedAt: day(3), Available: 10},
			},
			wantPicks: [][2]int64{{2, 10}, {3, 5}},
		},
		{
			name:     "FIFO ties broken by stock row id",
			strategy: FIFO, qty: 4,
			cands: []Candidate{
				{StockItemID: 9, ReceivedAt: day(1), Available: 3},
				{StockItemID: 4, ReceivedAt: day(1), Available: 3},
			},
			wantPicks: [][2]int64{{4, 3}, {9, 1}},
		},
		{
			name:     "FEFO takes first expiry even if received later",
			strategy: FEFO, qty: 12,
			cands: []Candidate{
				{StockItemID: 1, Expiry: dp(2026, 12, 1), ReceivedAt: day(1), Available: 10},
				{StockItemID: 2, Expiry: dp(2026, 7, 1), ReceivedAt: day(9), Available: 10},
			},
			wantPicks: [][2]int64{{2, 10}, {1, 2}},
		},
		{
			name:     "FEFO skips expired lots but allows expiry today",
			strategy: FEFO, qty: 8,
			cands: []Candidate{
				{StockItemID: 1, Expiry: dp(2026, 5, 31), Available: 100},
				{StockItemID: 2, Expiry: dp(2026, 6, 1), Available: 5},
				{StockItemID: 3, Expiry: dp(2026, 6, 20), Available: 5},
			},
			wantPicks: [][2]int64{{2, 5}, {3, 3}},
		},
		{
			name:     "FEFO puts rows without expiry last",
			strategy: FEFO, qty: 6,
			cands: []Candidate{
				{StockItemID: 1, ReceivedAt: day(0), Available: 5},
				{StockItemID: 2, Expiry: dp(2027, 1, 1), ReceivedAt: day(5), Available: 5},
			},
			wantPicks: [][2]int64{{2, 5}, {1, 1}},
		},
		{
			name:     "FEFO same expiry falls back to FIFO",
			strategy: FEFO, qty: 3,
			cands: []Candidate{
				{StockItemID: 1, Expiry: dp(2026, 9, 1), ReceivedAt: day(4), Available: 5},
				{StockItemID: 2, Expiry: dp(2026, 9, 1), ReceivedAt: day(2), Available: 5},
			},
			wantPicks: [][2]int64{{2, 3}},
		},
		{
			name:     "shortage is reported",
			strategy: FIFO, qty: 30,
			cands: []Candidate{
				{StockItemID: 1, Available: 10},
				{StockItemID: 2, Available: 0},
				{StockItemID: 3, Available: -2},
			},
			wantPicks: [][2]int64{{1, 10}},
			wantShort: 20,
		},
		{
			name:      "no candidates",
			strategy:  FEFO,
			qty:       5,
			wantPicks: [][2]int64{},
			wantShort: 5,
		},
		{
			name:     "expired stock does not matter for FIFO products",
			strategy: FIFO, qty: 2,
			cands:     []Candidate{{StockItemID: 1, Expiry: dp(2020, 1, 1), Available: 2}},
			wantPicks: [][2]int64{{1, 2}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]Candidate(nil), tc.cands...)
			picks, short := Allocate(tc.qty, tc.cands, tc.strategy, today)
			if got := picked(picks); !reflect.DeepEqual(got, tc.wantPicks) {
				t.Errorf("picks = %v, want %v", got, tc.wantPicks)
			}
			if short != tc.wantShort {
				t.Errorf("shortage = %d, want %d", short, tc.wantShort)
			}
			if !reflect.DeepEqual(before, tc.cands) {
				t.Error("Allocate modified its input")
			}
		})
	}
}

func TestAllocateDemandsSharesPool(t *testing.T) {
	pool := map[int64][]Candidate{
		100: {
			{StockItemID: 1, ReceivedAt: day(1), Available: 6},
			{StockItemID: 2, ReceivedAt: day(2), Available: 6},
		},
		200: {{StockItemID: 3, Expiry: dp(2026, 8, 1), Available: 4}},
	}
	demands := []Demand{
		{Key: 10, ProductID: 100, Quantity: 8, Strategy: FIFO},
		{Key: 11, ProductID: 100, Quantity: 8, Strategy: FIFO},
		{Key: 12, ProductID: 200, Quantity: 3, Strategy: FEFO},
	}
	res := AllocateDemands(demands, pool, NewDate(2026, 6, 1))
	if len(res) != 3 {
		t.Fatalf("got %d results", len(res))
	}
	if got := picked(res[0].Picks); !reflect.DeepEqual(got, [][2]int64{{1, 6}, {2, 2}}) || res[0].Shortage != 0 {
		t.Errorf("line 10: picks %v short %d", got, res[0].Shortage)
	}
	if got := picked(res[1].Picks); !reflect.DeepEqual(got, [][2]int64{{2, 4}}) || res[1].Shortage != 4 || res[1].Allocated != 4 {
		t.Errorf("line 11: picks %v short %d", got, res[1].Shortage)
	}
	if res[2].Allocated != 3 {
		t.Errorf("line 12 allocated %d", res[2].Allocated)
	}
	if pool[100][0].Available != 6 {
		t.Error("AllocateDemands modified the pool")
	}
}

func TestStrategyFor(t *testing.T) {
	if StrategyFor(TrackingLotExpiry) != FEFO || StrategyFor(TrackingLot) != FIFO || StrategyFor(TrackingNone) != FIFO {
		t.Error("wrong strategy mapping")
	}
}
