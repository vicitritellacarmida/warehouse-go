package domain

// StockGrouping is the level of detail of the stock report.
type StockGrouping string

// Stock report groupings.
const (
	GroupByProduct   StockGrouping = "product"
	GroupByWarehouse StockGrouping = "warehouse"
	GroupByLocation  StockGrouping = "location"
	GroupByLot       StockGrouping = "lot"
)

// Valid reports whether g is a known grouping.
func (g StockGrouping) Valid() bool {
	switch g {
	case GroupByProduct, GroupByWarehouse, GroupByLocation, GroupByLot:
		return true
	}
	return false
}

// StockRow is one row of the stock report. Fields that are not part of the
// chosen grouping are left empty.
type StockRow struct {
	ProductID    int64  `json:"product_id"`
	SKU          string `json:"sku"`
	WarehouseID  *int64 `json:"warehouse_id,omitempty"`
	LocationID   *int64 `json:"location_id,omitempty"`
	LocationCode string `json:"location_code,omitempty"`
	LocationType string `json:"location_type,omitempty"`
	LotID        *int64 `json:"lot_id,omitempty"`
	LotNumber    string `json:"lot_number,omitempty"`
	ExpiryDate   *Date  `json:"expiry_date,omitempty"`
	OnHand       int64  `json:"on_hand"`
	Reserved     int64  `json:"reserved"`
	Available    int64  `json:"available"`
	Quarantined  int64  `json:"quarantined"`
}

// ExpiringRow is stock of a lot that expires within the report window.
type ExpiringRow struct {
	ProductID    int64  `json:"product_id"`
	SKU          string `json:"sku"`
	WarehouseID  int64  `json:"warehouse_id"`
	LocationID   int64  `json:"location_id"`
	LocationCode string `json:"location_code"`
	LotID        int64  `json:"lot_id"`
	LotNumber    string `json:"lot_number"`
	ExpiryDate   Date   `json:"expiry_date"`
	DaysLeft     int    `json:"days_left"`
	Expired      bool   `json:"expired"`
	OnHand       int64  `json:"on_hand"`
}

// LowStockRow is a product whose available stock in a warehouse is at or
// below its reorder point.
type LowStockRow struct {
	ProductID    int64  `json:"product_id"`
	SKU          string `json:"sku"`
	WarehouseID  int64  `json:"warehouse_id"`
	ReorderPoint int64  `json:"reorder_point"`
	Available    int64  `json:"available"`
	Shortfall    int64  `json:"shortfall"`
}

// LedgerMismatch is a stock row whose on-hand quantity differs from the sum
// of its ledger movements.
type LedgerMismatch struct {
	StockItemID int64  `json:"stock_item_id"`
	ProductID   int64  `json:"product_id"`
	LocationID  int64  `json:"location_id"`
	LotID       *int64 `json:"lot_id,omitempty"`
	OnHand      int64  `json:"on_hand"`
	LedgerQty   int64  `json:"ledger_qty"`
}

// CostMismatch is a product/warehouse whose cost layers do not hold the
// same quantity as its on-hand stock.
type CostMismatch struct {
	ProductID   int64 `json:"product_id"`
	WarehouseID int64 `json:"warehouse_id"`
	OnHand      int64 `json:"on_hand"`
	LayerQty    int64 `json:"layer_qty"`
}

// Reconciliation is the result of checking the derived tables against the
// ledger. Both lists are empty when everything is consistent.
type Reconciliation struct {
	LedgerMismatches []LedgerMismatch `json:"ledger_mismatches"`
	CostMismatches   []CostMismatch   `json:"cost_mismatches"`
}

// Consistent reports whether no mismatches were found.
func (r Reconciliation) Consistent() bool {
	return len(r.LedgerMismatches) == 0 && len(r.CostMismatches) == 0
}
