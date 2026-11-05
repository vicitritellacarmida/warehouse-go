package domain

import "time"

// AdjustmentReason explains why a cycle count changed stock.
type AdjustmentReason string

// Adjustment reasons accepted when posting a cycle count.
const (
	ReasonDamaged  AdjustmentReason = "damaged"
	ReasonLost     AdjustmentReason = "lost"
	ReasonFound    AdjustmentReason = "found"
	ReasonMiscount AdjustmentReason = "miscount"
	ReasonExpired  AdjustmentReason = "expired"
	ReasonOther    AdjustmentReason = "other"
)

// Valid reports whether r is a known reason.
func (r AdjustmentReason) Valid() bool {
	switch r {
	case ReasonDamaged, ReasonLost, ReasonFound, ReasonMiscount, ReasonExpired, ReasonOther:
		return true
	}
	return false
}

// CycleCount is a physical count of one location. While it is open the
// location is blocked for movements.
type CycleCount struct {
	ID          int64            `json:"id"`
	LocationID  int64            `json:"location_id"`
	WarehouseID int64            `json:"warehouse_id"`
	Status      CountStatus      `json:"status"`
	Reason      AdjustmentReason `json:"reason,omitempty"`
	Note        string           `json:"note,omitempty"`
	Lines       []CountLine      `json:"lines"`
	CreatedAt   time.Time        `json:"created_at"`
	PostedAt    *time.Time       `json:"posted_at,omitempty"`
}

// CountLine compares the system quantity of one product and lot with the
// counted quantity. SystemQty is captured when the count opens.
type CountLine struct {
	ID         int64  `json:"id"`
	CountID    int64  `json:"cycle_count_id"`
	ProductID  int64  `json:"product_id"`
	LotID      *int64 `json:"lot_id,omitempty"`
	SystemQty  int64  `json:"system_qty"`
	CountedQty *int64 `json:"counted_qty,omitempty"`
	Variance   *int64 `json:"variance,omitempty"`
}

// ComputeVariance sets Variance = CountedQty - SystemQty when counted.
func (l *CountLine) ComputeVariance() {
	if l.CountedQty == nil {
		l.Variance = nil
		return
	}
	v := *l.CountedQty - l.SystemQty
	l.Variance = &v
}
