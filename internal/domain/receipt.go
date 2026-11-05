package domain

import (
	"strings"
	"time"
)

// Receipt is an expected delivery from a supplier into one warehouse.
// ToleranceBP is the allowed over- and under-receipt in basis points
// (100 bp = 1%).
type Receipt struct {
	ID          int64         `json:"id"`
	WarehouseID int64         `json:"warehouse_id"`
	Reference   string        `json:"reference"`
	Supplier    string        `json:"supplier"`
	Status      ReceiptStatus `json:"status"`
	ToleranceBP int64         `json:"tolerance_bp"`
	Lines       []ReceiptLine `json:"lines"`
	CreatedAt   time.Time     `json:"created_at"`
	ClosedAt    *time.Time    `json:"closed_at,omitempty"`
}

// ReceiptLine is one expected product on a receipt. Quantities are in base
// units and UnitCost is in IDR minor units per base unit.
type ReceiptLine struct {
	ID          int64  `json:"id"`
	ReceiptID   int64  `json:"receipt_id"`
	ProductID   int64  `json:"product_id"`
	ExpectedQty int64  `json:"expected_qty"`
	ReceivedQty int64  `json:"received_qty"`
	UnitCost    int64  `json:"unit_cost"`
	Outcome     string `json:"outcome"`
}

// Validate checks receipt header fields.
func (r *Receipt) Validate() error {
	r.Reference = strings.TrimSpace(r.Reference)
	r.Supplier = strings.TrimSpace(r.Supplier)
	switch {
	case r.Reference == "" || len(r.Reference) > 64:
		return Invalid(CodeValidation, "reference is required and must be at most 64 characters")
	case r.WarehouseID <= 0:
		return Invalid(CodeValidation, "warehouse_id is required")
	case r.ToleranceBP < 0 || r.ToleranceBP > 10000:
		return Invalid(CodeValidation, "tolerance_bp must be between 0 and 10000")
	case len(r.Lines) == 0:
		return Invalid(CodeValidation, "a receipt needs at least one line")
	}
	return nil
}

// Line returns a pointer to the line with the given id, or nil.
func (r *Receipt) Line(id int64) *ReceiptLine {
	for i := range r.Lines {
		if r.Lines[i].ID == id {
			return &r.Lines[i]
		}
	}
	return nil
}

// Receipt line outcomes, computed from expected vs received quantities.
const (
	OutcomePending  = "pending"  // nothing received yet
	OutcomeShort    = "short"    // below expected minus tolerance
	OutcomeComplete = "complete" // within tolerance
)

// tolerance returns the tolerance in units for an expected quantity,
// rounded down so that small lines get no free over-receipt.
func tolerance(expected, bp int64) int64 {
	// expected*bp can overflow for huge quantities; divide first in that case.
	if expected > 1<<40 {
		return expected / 10000 * bp
	}
	return expected * bp / 10000
}

// MaxReceivable is the most that may be received on a line.
func MaxReceivable(expected, bp int64) int64 { return expected + tolerance(expected, bp) }

// MinAcceptable is the least that must be received for the line to count
// as complete without explicit acceptance of a short receipt.
func MinAcceptable(expected, bp int64) int64 { return expected - tolerance(expected, bp) }

// CheckReceive returns an over_receipt error if receiving qty more units on
// a line would go past the tolerance.
func CheckReceive(line ReceiptLine, bp, qty int64) error {
	total, err := AddQty(line.ReceivedQty, qty)
	if err != nil {
		return err
	}
	if max := MaxReceivable(line.ExpectedQty, bp); total > max {
		return Invalid(CodeOverReceipt, "line %d would receive %d, maximum is %d", line.ID, total, max).
			With("line_id", line.ID).With("max_receivable", max).With("already_received", line.ReceivedQty)
	}
	return nil
}

// LineOutcome classifies a receipt line.
func LineOutcome(line ReceiptLine, bp int64) string {
	switch {
	case line.ReceivedQty == 0:
		return OutcomePending
	case line.ReceivedQty < MinAcceptable(line.ExpectedQty, bp):
		return OutcomeShort
	}
	return OutcomeComplete
}
