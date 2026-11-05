package domain

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func code(err error) string {
	var de *Error
	if errors.As(err, &de) {
		return de.Code
	}
	if err != nil {
		return "other"
	}
	return ""
}

func TestProductValidateAndConvert(t *testing.T) {
	p := &Product{SKU: " SKU-1 ", Name: "Sweet tea 330ml", BaseUOM: "pcs",
		Units: []Unit{{UOM: "box", Factor: 12}, {UOM: "PCS", Factor: 1}, {UOM: "pallet", Factor: 480}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.SKU != "SKU-1" || p.BaseUOM != "PCS" || p.TrackingMode != TrackingNone {
		t.Fatalf("not normalized: %+v", p)
	}
	want := []Unit{{"PCS", 1}, {"BOX", 12}, {"PALLET", 480}}
	if !reflect.DeepEqual(p.Units, want) {
		t.Fatalf("units = %v", p.Units)
	}
	tests := []struct {
		qty  int64
		uom  string
		want int64
		code string
	}{
		{3, "box", 36, ""},
		{3, "", 3, ""},
		{2, "PALLET", 960, ""},
		{1, "crate", 0, CodeUnknownUnit},
		{0, "box", 0, CodeValidation},
		{-1, "", 0, CodeValidation},
		{math.MaxInt64 / 10, "box", 0, CodeQuantityOverflow},
	}
	for _, tc := range tests {
		got, err := p.ToBase(tc.qty, tc.uom)
		if code(err) != tc.code || got != tc.want {
			t.Errorf("ToBase(%d, %q) = %d, %v; want %d, %s", tc.qty, tc.uom, got, err, tc.want, tc.code)
		}
	}
}

func TestProductValidateRejects(t *testing.T) {
	cases := []Product{
		{Name: "x", BaseUOM: "PCS"},
		{SKU: "a", BaseUOM: "PCS"},
		{SKU: "a", Name: "x"},
		{SKU: "a", Name: "x", BaseUOM: "PCS", TrackingMode: "serial"},
		{SKU: "a", Name: "x", BaseUOM: "PCS", Units: []Unit{{"PCS", 2}}},
		{SKU: "a", Name: "x", BaseUOM: "PCS", Units: []Unit{{"BOX", 0}}},
		{SKU: "a", Name: "x", BaseUOM: "PCS", Units: []Unit{{"BOX", 6}, {"box", 12}}},
	}
	for i, p := range cases {
		if err := p.Validate(); code(err) != CodeValidation {
			t.Errorf("case %d: expected validation error, got %v", i, err)
		}
	}
}

func TestValidateLotInput(t *testing.T) {
	none := &Product{SKU: "N", TrackingMode: TrackingNone}
	lot := &Product{SKU: "L", TrackingMode: TrackingLot}
	exp := &Product{SKU: "E", TrackingMode: TrackingLotExpiry}
	d := dp(2027, 1, 1)
	tests := []struct {
		p    *Product
		lot  string
		exp  *Date
		code string
	}{
		{none, "", nil, ""},
		{none, "A1", nil, CodeLotNotAllowed},
		{lot, "", nil, CodeLotRequired},
		{lot, "A1", nil, ""},
		{lot, "A1", d, CodeLotNotAllowed},
		{exp, "A1", nil, CodeExpiryRequired},
		{exp, "A1", d, ""},
	}
	for i, tc := range tests {
		if got := code(ValidateLotInput(tc.p, tc.lot, tc.exp)); got != tc.code {
			t.Errorf("case %d: code %q, want %q", i, got, tc.code)
		}
	}
}

func TestOrderStateMachine(t *testing.T) {
	allowed := map[[2]OrderStatus]bool{
		{OrderNew, OrderAllocated}: true, {OrderNew, OrderCancelled}: true,
		{OrderAllocated, OrderPicked}: true, {OrderAllocated, OrderCancelled}: true,
		{OrderPicked, OrderPacked}: true, {OrderPicked, OrderCancelled}: true,
		{OrderPacked, OrderShipped}: true, {OrderPacked, OrderCancelled}: true,
	}
	all := []OrderStatus{OrderNew, OrderAllocated, OrderPicked, OrderPacked, OrderShipped, OrderCancelled}
	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]OrderStatus{from, to}]
			if got := from.CanTransition(to); got != want {
				t.Errorf("%s -> %s: got %v, want %v", from, to, got, want)
			}
			if err := from.CheckTransition(to); (err == nil) != want || (err != nil && code(err) != CodeInvalidTransition) {
				t.Errorf("%s -> %s: CheckTransition = %v", from, to, err)
			}
		}
	}
}

func TestLineAndDocumentStateMachines(t *testing.T) {
	if !LineOpen.CanTransition(LineBackordered) || LineShipped.CanTransition(LineCancelled) || LineBackordered.CanTransition(LineAllocated) {
		t.Error("line transitions wrong")
	}
	checks := []struct {
		err  error
		want bool
	}{
		{ReceiptOpen.CheckTransition(ReceiptReceiving), true},
		{ReceiptReceiving.CheckTransition(ReceiptReceiving), true},
		{ReceiptReceiving.CheckTransition(ReceiptCancelled), false},
		{ReceiptClosed.CheckTransition(ReceiptReceiving), false},
		{TransferDraft.CheckTransition(TransferInTransit), true},
		{TransferInTransit.CheckTransition(TransferCancelled), false},
		{TransferInTransit.CheckTransition(TransferDiscrepancy), true},
		{TransferReceived.CheckTransition(TransferInTransit), false},
		{CountOpen.CheckTransition(CountPosted), true},
		{CountPosted.CheckTransition(CountCancelled), false},
	}
	for i, c := range checks {
		if (c.err == nil) != c.want {
			t.Errorf("check %d: err = %v, want allowed=%v", i, c.err, c.want)
		}
	}
}

func TestReceiptTolerance(t *testing.T) {
	const bp = 500 // 5%
	line := ReceiptLine{ID: 1, ExpectedQty: 100}
	if MaxReceivable(100, bp) != 105 || MinAcceptable(100, bp) != 95 {
		t.Fatal("bad tolerance bounds")
	}
	if MaxReceivable(10, bp) != 10 {
		t.Error("small lines must not get rounding tolerance")
	}
	if err := CheckReceive(line, bp, 105); err != nil {
		t.Errorf("105 should be accepted: %v", err)
	}
	if code(CheckReceive(line, bp, 106)) != CodeOverReceipt {
		t.Error("106 should be over receipt")
	}
	line.ReceivedQty = 100
	if code(CheckReceive(line, bp, 6)) != CodeOverReceipt {
		t.Error("cumulative over receipt not detected")
	}
	outcomes := map[int64]string{0: OutcomePending, 50: OutcomeShort, 94: OutcomeShort, 95: OutcomeComplete, 105: OutcomeComplete}
	for received, want := range outcomes {
		if got := LineOutcome(ReceiptLine{ExpectedQty: 100, ReceivedQty: received}, bp); got != want {
			t.Errorf("received %d: outcome %s, want %s", received, got, want)
		}
	}
}

func TestLocationRules(t *testing.T) {
	l := &Location{ID: 1, Code: "A-01", Capacity: ip(100)}
	if err := l.CheckCapacity(90, 10); err != nil {
		t.Error(err)
	}
	if code(l.CheckCapacity(90, 11)) != CodeCapacityExceeded {
		t.Error("capacity not enforced")
	}
	l.OpenCountID = ip(7)
	if code(l.CheckNotCounting()) != CodeLocationCounting {
		t.Error("count block not enforced")
	}
	if LocationQuarantine.CountsAsAvailable() || LocationReceiving.Allocatable() || !LocationPicking.Allocatable() {
		t.Error("location type rules wrong")
	}
	s := StockItem{OnHand: 10, Reserved: 3, LocationType: LocationQuarantine}
	if s.Available() != 0 {
		t.Error("quarantine stock must not be available")
	}
	s.LocationType = LocationStorage
	if s.Available() != 7 {
		t.Error("available = on hand - reserved")
	}
}

func TestDistributeAndDiscrepancy(t *testing.T) {
	parts := []TransferLinePart{{Quantity: 4, UnitCost: 100}, {Quantity: 6, UnitCost: 200}}
	if got := Distribute(parts, 7); !reflect.DeepEqual(got, []int64{4, 3}) {
		t.Errorf("Distribute = %v", got)
	}
	parts[0].QtyReceived, parts[1].QtyReceived = 4, 3
	l := TransferLine{QtyShipped: 10, QtyReceived: 7, Costs: parts}
	l.ComputeDiscrepancy()
	if l.Discrepancy != 3 || l.DiscrepancyValue != 600 {
		t.Errorf("discrepancy = %d value %d", l.Discrepancy, l.DiscrepancyValue)
	}
	defer func() {
		if recover() == nil {
			t.Error("expected panic when receiving more than shipped")
		}
	}()
	Distribute(parts, 11)
}

func TestDateJSON(t *testing.T) {
	d := NewDate(2026, 3, 9)
	b, _ := json.Marshal(d)
	if string(b) != `"2026-03-09"` {
		t.Fatalf("marshal = %s", b)
	}
	var back Date
	if err := json.Unmarshal(b, &back); err != nil || !back.Equal(d) {
		t.Fatalf("unmarshal = %v, %v", back, err)
	}
	if err := json.Unmarshal([]byte(`"09/03/2026"`), &back); err == nil {
		t.Error("expected parse error")
	}
	if d.DaysUntil(d.AddDays(10)) != 10 {
		t.Error("DaysUntil wrong")
	}
}

func TestCountVariance(t *testing.T) {
	l := CountLine{SystemQty: 10}
	l.ComputeVariance()
	if l.Variance != nil {
		t.Error("uncounted line has no variance")
	}
	l.CountedQty = ip(7)
	l.ComputeVariance()
	if *l.Variance != -3 {
		t.Errorf("variance = %d", *l.Variance)
	}
	if !ReasonDamaged.Valid() || AdjustmentReason("whim").Valid() {
		t.Error("reason validation wrong")
	}
}
