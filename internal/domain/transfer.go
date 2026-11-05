package domain

import "time"

// Transfer moves stock from one warehouse to another. Between ship and
// receive the stock is in transit: it is in no location and in neither
// warehouse's on-hand quantity.
type Transfer struct {
	ID              int64          `json:"id"`
	FromWarehouseID int64          `json:"from_warehouse_id"`
	ToWarehouseID   int64          `json:"to_warehouse_id"`
	Status          TransferStatus `json:"status"`
	Lines           []TransferLine `json:"lines"`
	CreatedAt       time.Time      `json:"created_at"`
	ShippedAt       *time.Time     `json:"shipped_at,omitempty"`
	ReceivedAt      *time.Time     `json:"received_at,omitempty"`
}

// TransferLine is one product on a transfer. Lots and Costs record what was
// shipped so the destination can receive the same lots at the same cost.
type TransferLine struct {
	ID               int64              `json:"id"`
	TransferID       int64              `json:"transfer_id"`
	ProductID        int64              `json:"product_id"`
	Quantity         int64              `json:"quantity"`
	QtyShipped       int64              `json:"qty_shipped"`
	QtyReceived      int64              `json:"qty_received"`
	Discrepancy      int64              `json:"discrepancy"`
	DiscrepancyValue int64              `json:"discrepancy_value"`
	Lots             []TransferLinePart `json:"lots"`
	Costs            []TransferLinePart `json:"costs"`
}

// TransferLinePart is a shipped slice of a transfer line: either one lot
// (LotID, ReceivedAt) or one cost (UnitCost).
type TransferLinePart struct {
	ID          int64     `json:"id"`
	LotID       *int64    `json:"lot_id,omitempty"`
	UnitCost    int64     `json:"unit_cost,omitempty"`
	ReceivedAt  time.Time `json:"received_at,omitempty"`
	Quantity    int64     `json:"quantity"`
	QtyReceived int64     `json:"qty_received"`
}

// Line returns a pointer to the line with the given id, or nil.
func (t *Transfer) Line(id int64) *TransferLine {
	for i := range t.Lines {
		if t.Lines[i].ID == id {
			return &t.Lines[i]
		}
	}
	return nil
}

// Distribute spreads received units over parts in order, filling each part
// up to its quantity. It returns how much each part receives. It panics if
// received exceeds the total, so callers must check first.
func Distribute(parts []TransferLinePart, received int64) []int64 {
	out := make([]int64, len(parts))
	for i, p := range parts {
		take := min(received, p.Quantity)
		out[i] = take
		received -= take
	}
	if received > 0 {
		panic("domain.Distribute: received exceeds shipped")
	}
	return out
}

// ComputeDiscrepancy fills Discrepancy (units shipped but not received) and
// DiscrepancyValue (their FIFO cost) on a line. Call it only once the
// transfer has been received; before that every shipped unit is simply in
// transit.
func (l *TransferLine) ComputeDiscrepancy() {
	l.Discrepancy = l.QtyShipped - l.QtyReceived
	l.DiscrepancyValue = 0
	for _, c := range l.Costs {
		l.DiscrepancyValue += (c.Quantity - c.QtyReceived) * c.UnitCost
	}
}
