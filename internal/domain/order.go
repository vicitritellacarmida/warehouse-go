package domain

import (
	"strings"
	"time"
)

// SalesOrder is a customer order shipped from one warehouse.
// BackorderOfID is set when the order was created automatically to hold
// quantities that could not be allocated on the original order.
type SalesOrder struct {
	ID            int64       `json:"id"`
	OrderNumber   string      `json:"order_number"`
	WarehouseID   int64       `json:"warehouse_id"`
	Customer      string      `json:"customer"`
	Status        OrderStatus `json:"status"`
	BackorderOfID *int64      `json:"backorder_of_id,omitempty"`
	Lines         []OrderLine `json:"lines"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

// OrderLine is one product on a sales order. Quantities are in base units.
// Quantity is what the customer asked for; QtyBackordered is the part moved
// to a backorder because stock was short. The line is complete when
// QtyShipped == Quantity - QtyBackordered.
type OrderLine struct {
	ID             int64      `json:"id"`
	OrderID        int64      `json:"order_id"`
	ProductID      int64      `json:"product_id"`
	Quantity       int64      `json:"quantity"`
	QtyAllocated   int64      `json:"qty_allocated"`
	QtyBackordered int64      `json:"qty_backordered"`
	QtyPicked      int64      `json:"qty_picked"`
	QtyShipped     int64      `json:"qty_shipped"`
	Status         LineStatus `json:"status"`
}

// Validate checks order header fields.
func (o *SalesOrder) Validate() error {
	o.OrderNumber = strings.TrimSpace(o.OrderNumber)
	o.Customer = strings.TrimSpace(o.Customer)
	switch {
	case o.OrderNumber == "" || len(o.OrderNumber) > 64:
		return Invalid(CodeValidation, "order_number is required and must be at most 64 characters")
	case o.WarehouseID <= 0:
		return Invalid(CodeValidation, "warehouse_id is required")
	case len(o.Lines) == 0:
		return Invalid(CodeValidation, "an order needs at least one line")
	}
	return nil
}

// Line returns a pointer to the line with the given id, or nil.
func (o *SalesOrder) Line(id int64) *OrderLine {
	for i := range o.Lines {
		if o.Lines[i].ID == id {
			return &o.Lines[i]
		}
	}
	return nil
}

// AllocationStatus is the state of a reservation against a stock row.
type AllocationStatus string

// Allocation statuses.
const (
	AllocReserved AllocationStatus = "reserved" // reserved in a storage/picking location
	AllocPicked   AllocationStatus = "picked"   // moved to a shipping location, still reserved
	AllocShipped  AllocationStatus = "shipped"
	AllocReleased AllocationStatus = "released" // reservation released by cancel
)

// Allocation reserves Quantity units of one stock row for an order line.
// StockItemID and LocationID always point to where the reserved units are
// now: the source bin before picking, the shipping bin after.
type Allocation struct {
	ID          int64            `json:"id"`
	OrderID     int64            `json:"order_id"`
	OrderLineID int64            `json:"order_line_id"`
	ProductID   int64            `json:"product_id"`
	StockItemID int64            `json:"stock_item_id"`
	LocationID  int64            `json:"location_id"`
	LotID       *int64           `json:"lot_id,omitempty"`
	Quantity    int64            `json:"quantity"`
	Status      AllocationStatus `json:"status"`
}

// PickTask is one instruction on a pick list.
type PickTask struct {
	AllocationID int64  `json:"allocation_id"`
	OrderLineID  int64  `json:"order_line_id"`
	ProductID    int64  `json:"product_id"`
	SKU          string `json:"sku"`
	LocationID   int64  `json:"location_id"`
	LocationCode string `json:"location_code"`
	LotID        *int64 `json:"lot_id,omitempty"`
	LotNumber    string `json:"lot_number,omitempty"`
	ExpiryDate   *Date  `json:"expiry_date,omitempty"`
	Quantity     int64  `json:"quantity"`
	Status       string `json:"status"`
}

// Shipment records goods leaving the warehouse for an order, with the cost
// of goods sold (COGS) in IDR minor units.
type Shipment struct {
	ID        int64          `json:"id"`
	OrderID   int64          `json:"order_id"`
	TotalCOGS int64          `json:"total_cogs"`
	Lines     []ShipmentLine `json:"lines"`
	ShippedAt time.Time      `json:"shipped_at"`
}

// ShipmentLine is the quantity of one lot shipped for one order line, with
// its FIFO cost.
type ShipmentLine struct {
	ID          int64  `json:"id"`
	OrderLineID int64  `json:"order_line_id"`
	ProductID   int64  `json:"product_id"`
	LotID       *int64 `json:"lot_id,omitempty"`
	Quantity    int64  `json:"quantity"`
	COGS        int64  `json:"cogs"`
}
