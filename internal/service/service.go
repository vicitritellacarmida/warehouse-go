// Package service implements the warehouse use cases. Each write use case
// runs in one database transaction, loads what it needs with row locks,
// asks the domain package for decisions and writes the results.
//
// Lock order, used everywhere to avoid deadlocks:
//
//  1. the document row (order, receipt, transfer, cycle count)
//  2. locations, by id
//  3. stock rows, by id
//  4. cost layers, oldest first
package service

import (
	"context"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// Options configures a Service.
type Options struct {
	// Now returns the current time. It defaults to time.Now.
	Now func() time.Time
	// DefaultToleranceBP is used for receipts that do not set a tolerance.
	DefaultToleranceBP int64
}

// Service exposes the warehouse use cases.
type Service struct {
	store *store.Store
	clock func() time.Time
	tolBP int64
}

// New creates a Service on top of a Store.
func New(st *store.Store, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{store: st, clock: opts.Now, tolBP: opts.DefaultToleranceBP}
}

// now returns the current UTC time truncated to what MySQL DATETIME(6)
// stores, so values read back compare equal.
func (s *Service) now() time.Time { return s.clock().UTC().Truncate(time.Microsecond) }

func (s *Service) today() domain.Date { return domain.DateOf(s.now()) }

// Store returns the underlying store (used by the HTTP layer for
// idempotency records).
func (s *Service) Store() *store.Store { return s.store }

// QtyInput is a quantity in an optional unit of measure. An empty UOM means
// the product's base unit.
type QtyInput struct {
	Quantity int64  `json:"quantity"`
	UOM      string `json:"uom,omitempty"`
}

// productIDs returns the distinct product ids of a list of items.
func productIDs[T any](items []T, id func(T) int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, it := range items {
		if pid := id(it); !seen[pid] {
			seen[pid] = true
			out = append(out, pid)
		}
	}
	return out
}

// requireLocation loads a location and checks its warehouse and type.
func requireLocation(ctx context.Context, q *store.Queries, id, warehouseID int64, types ...domain.LocationType) (*domain.Location, error) {
	loc, err := q.GetLocation(ctx, id)
	if err != nil {
		return nil, err
	}
	if warehouseID > 0 && loc.WarehouseID != warehouseID {
		return nil, domain.Invalid(domain.CodeWrongWarehouse, "location %s is not in warehouse %d", loc.Code, warehouseID).
			With("location_id", id)
	}
	if len(types) > 0 {
		for _, t := range types {
			if loc.Type == t {
				return loc, nil
			}
		}
		return nil, domain.Invalid(domain.CodeWrongLocationType, "location %s has type %s, which is not allowed here", loc.Code, loc.Type).
			With("location_id", id).With("allowed", types)
	}
	return loc, nil
}
