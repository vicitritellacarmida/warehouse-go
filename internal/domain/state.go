package domain

import "slices"

// transitions maps each status to the statuses it may move to.
type transitions[S ~string] map[S][]S

func (t transitions[S]) can(from, to S) bool { return slices.Contains(t[from], to) }

func (t transitions[S]) check(entity string, from, to S) error {
	if t.can(from, to) {
		return nil
	}
	return Conflict(CodeInvalidTransition, "%s cannot move from %s to %s", entity, from, to).
		With("from", string(from)).With("to", string(to))
}

// OrderStatus is the lifecycle state of a sales order.
type OrderStatus string

// Sales order statuses.
const (
	OrderNew       OrderStatus = "new"
	OrderAllocated OrderStatus = "allocated"
	OrderPicked    OrderStatus = "picked"
	OrderPacked    OrderStatus = "packed"
	OrderShipped   OrderStatus = "shipped"
	OrderCancelled OrderStatus = "cancelled"
)

var orderTransitions = transitions[OrderStatus]{
	OrderNew:       {OrderAllocated, OrderCancelled},
	OrderAllocated: {OrderPicked, OrderCancelled},
	OrderPicked:    {OrderPacked, OrderCancelled},
	OrderPacked:    {OrderShipped, OrderCancelled},
}

// CanTransition reports whether an order may move from s to to.
func (s OrderStatus) CanTransition(to OrderStatus) bool { return orderTransitions.can(s, to) }

// CheckTransition returns an invalid_transition error if s cannot move to to.
func (s OrderStatus) CheckTransition(to OrderStatus) error {
	return orderTransitions.check("order", s, to)
}

// LineStatus is the lifecycle state of a sales order line.
type LineStatus string

// Sales order line statuses.
const (
	LineOpen        LineStatus = "open"
	LineAllocated   LineStatus = "allocated"
	LineBackordered LineStatus = "backordered" // nothing allocated; whole quantity moved to a backorder
	LinePicked      LineStatus = "picked"
	LineShipped     LineStatus = "shipped"
	LineCancelled   LineStatus = "cancelled"
)

var lineTransitions = transitions[LineStatus]{
	LineOpen:      {LineAllocated, LineBackordered, LineCancelled},
	LineAllocated: {LinePicked, LineCancelled},
	LinePicked:    {LineShipped, LineCancelled},
}

// CanTransition reports whether a line may move from s to to.
func (s LineStatus) CanTransition(to LineStatus) bool { return lineTransitions.can(s, to) }

// CheckTransition returns an invalid_transition error if s cannot move to to.
func (s LineStatus) CheckTransition(to LineStatus) error {
	return lineTransitions.check("order line", s, to)
}

// ReceiptStatus is the lifecycle state of a purchase receipt.
type ReceiptStatus string

// Purchase receipt statuses.
const (
	ReceiptOpen      ReceiptStatus = "open"
	ReceiptReceiving ReceiptStatus = "receiving"
	ReceiptClosed    ReceiptStatus = "closed"
	ReceiptCancelled ReceiptStatus = "cancelled"
)

var receiptTransitions = transitions[ReceiptStatus]{
	ReceiptOpen:      {ReceiptReceiving, ReceiptClosed, ReceiptCancelled},
	ReceiptReceiving: {ReceiptReceiving, ReceiptClosed},
}

// CheckTransition returns an invalid_transition error if s cannot move to to.
func (s ReceiptStatus) CheckTransition(to ReceiptStatus) error {
	return receiptTransitions.check("receipt", s, to)
}

// TransferStatus is the lifecycle state of an inter-warehouse transfer.
type TransferStatus string

// Transfer statuses.
const (
	TransferDraft       TransferStatus = "draft"
	TransferInTransit   TransferStatus = "in_transit"
	TransferReceived    TransferStatus = "received"
	TransferDiscrepancy TransferStatus = "received_with_discrepancy"
	TransferCancelled   TransferStatus = "cancelled"
)

var transferTransitions = transitions[TransferStatus]{
	TransferDraft:     {TransferInTransit, TransferCancelled},
	TransferInTransit: {TransferReceived, TransferDiscrepancy},
}

// CheckTransition returns an invalid_transition error if s cannot move to to.
func (s TransferStatus) CheckTransition(to TransferStatus) error {
	return transferTransitions.check("transfer", s, to)
}

// CountStatus is the lifecycle state of a cycle count.
type CountStatus string

// Cycle count statuses.
const (
	CountOpen      CountStatus = "open"
	CountPosted    CountStatus = "posted"
	CountCancelled CountStatus = "cancelled"
)

var countTransitions = transitions[CountStatus]{
	CountOpen: {CountPosted, CountCancelled},
}

// CheckTransition returns an invalid_transition error if s cannot move to to.
func (s CountStatus) CheckTransition(to CountStatus) error {
	return countTransitions.check("cycle count", s, to)
}
