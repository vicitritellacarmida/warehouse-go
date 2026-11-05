package domain

import (
	"slices"
	"time"
)

// CostLayer is a FIFO cost layer: a quantity of a product that entered a
// warehouse at one unit cost (IDR minor units per base unit). Shipping and
// negative adjustments consume layers oldest first.
type CostLayer struct {
	ID           int64     `json:"id"`
	ProductID    int64     `json:"product_id"`
	WarehouseID  int64     `json:"warehouse_id"`
	UnitCost     int64     `json:"unit_cost"`
	QtyReceived  int64     `json:"qty_received"`
	QtyRemaining int64     `json:"qty_remaining"`
	SourceType   string    `json:"source_type"`
	SourceID     int64     `json:"source_id"`
	ReceivedAt   time.Time `json:"received_at"`
}

// Consumption is the quantity taken from one cost layer.
type Consumption struct {
	LayerID  int64
	Quantity int64
	UnitCost int64
}

// Cost returns Quantity x UnitCost with overflow detection.
func (c Consumption) Cost() (int64, error) { return MulQty(c.Quantity, c.UnitCost) }

// SortLayers orders layers oldest first, by receipt time then id.
func SortLayers(layers []CostLayer) {
	slices.SortStableFunc(layers, func(a, b CostLayer) int {
		if c := a.ReceivedAt.Compare(b.ReceivedAt); c != 0 {
			return c
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

// ConsumeFIFO takes qty units from layers, oldest first, and returns the
// consumptions and their total cost. It fails with insufficient_cost_layers
// if the layers hold fewer than qty units. The input is not modified.
func ConsumeFIFO(layers []CostLayer, qty int64) ([]Consumption, int64, error) {
	if qty <= 0 {
		return nil, 0, Invalid(CodeValidation, "quantity must be positive")
	}
	sorted := slices.Clone(layers)
	SortLayers(sorted)
	var (
		out       []Consumption
		total     int64
		remaining = qty
	)
	for _, l := range sorted {
		if remaining == 0 {
			break
		}
		if l.QtyRemaining <= 0 {
			continue
		}
		c := Consumption{LayerID: l.ID, Quantity: min(remaining, l.QtyRemaining), UnitCost: l.UnitCost}
		cost, err := c.Cost()
		if err != nil {
			return nil, 0, err
		}
		if total, err = AddQty(total, cost); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
		remaining -= c.Quantity
	}
	if remaining > 0 {
		return nil, 0, Conflict(CodeInsufficientCost, "cost layers hold %d units, %d needed", qty-remaining, qty).
			With("needed", qty).With("available", qty-remaining)
	}
	return out, total, nil
}

// ValuationRow is the FIFO value of a product in a warehouse.
type ValuationRow struct {
	ProductID   int64  `json:"product_id"`
	SKU         string `json:"sku"`
	WarehouseID int64  `json:"warehouse_id"`
	Quantity    int64  `json:"quantity"`
	Value       int64  `json:"value"`
}
