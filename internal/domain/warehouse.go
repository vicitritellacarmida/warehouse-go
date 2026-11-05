package domain

import (
	"strings"
	"time"
)

// LocationType is the role a bin plays in the warehouse flow.
type LocationType string

// Location types.
const (
	LocationReceiving  LocationType = "receiving"
	LocationStorage    LocationType = "storage"
	LocationPicking    LocationType = "picking"
	LocationShipping   LocationType = "shipping"
	LocationQuarantine LocationType = "quarantine"
)

// Valid reports whether t is a known location type.
func (t LocationType) Valid() bool {
	switch t {
	case LocationReceiving, LocationStorage, LocationPicking, LocationShipping, LocationQuarantine:
		return true
	}
	return false
}

// Allocatable reports whether stock in this location type can be reserved
// for outbound orders and transfers. Stock must be put away first, and
// quarantine stock is never available.
func (t LocationType) Allocatable() bool { return t == LocationStorage || t == LocationPicking }

// CountsAsAvailable reports whether stock in this location type counts
// towards the available quantity in reports.
func (t LocationType) CountsAsAvailable() bool { return t != LocationQuarantine }

// Warehouse is a physical site that holds stock.
type Warehouse struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Validate checks and normalizes warehouse fields.
func (w *Warehouse) Validate() error {
	w.Code = strings.ToUpper(strings.TrimSpace(w.Code))
	w.Name = strings.TrimSpace(w.Name)
	if w.Code == "" || len(w.Code) > 32 {
		return Invalid(CodeValidation, "code is required and must be at most 32 characters")
	}
	if w.Name == "" {
		return Invalid(CodeValidation, "name is required")
	}
	return nil
}

// Location is a bin inside a warehouse. Capacity, when set, is the maximum
// total on-hand quantity in base units. OpenCountID is set while a cycle
// count is open; movements into or out of the location are then blocked.
type Location struct {
	ID          int64        `json:"id"`
	WarehouseID int64        `json:"warehouse_id"`
	Code        string       `json:"code"`
	Type        LocationType `json:"type"`
	Capacity    *int64       `json:"capacity,omitempty"`
	OpenCountID *int64       `json:"open_count_id,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
}

// Validate checks and normalizes location fields.
func (l *Location) Validate() error {
	l.Code = strings.ToUpper(strings.TrimSpace(l.Code))
	if l.Code == "" || len(l.Code) > 64 {
		return Invalid(CodeValidation, "code is required and must be at most 64 characters")
	}
	if !l.Type.Valid() {
		return Invalid(CodeValidation, "type must be one of receiving, storage, picking, shipping, quarantine")
	}
	if l.Capacity != nil && *l.Capacity <= 0 {
		return Invalid(CodeValidation, "capacity must be positive when set")
	}
	return nil
}

// CheckCapacity returns an error when adding incoming units to a location
// that already holds current units would exceed its capacity.
func (l *Location) CheckCapacity(current, incoming int64) error {
	if l.Capacity == nil {
		return nil
	}
	total, err := AddQty(current, incoming)
	if err != nil || total > *l.Capacity {
		return Conflict(CodeCapacityExceeded, "location %s capacity %d would be exceeded", l.Code, *l.Capacity).
			With("location_id", l.ID).With("current", current).With("incoming", incoming)
	}
	return nil
}

// CheckNotCounting returns an error when a cycle count is open on l.
func (l *Location) CheckNotCounting() error {
	if l.OpenCountID != nil {
		return Conflict(CodeLocationCounting, "location %s is blocked by open cycle count %d", l.Code, *l.OpenCountID).
			With("location_id", l.ID).With("cycle_count_id", *l.OpenCountID)
	}
	return nil
}
