package domain

import (
	"math"
	"strings"
	"time"
)

// TrackingMode says whether stock of a product is tracked by lot, and
// whether lots carry an expiry date.
type TrackingMode string

// Tracking modes.
const (
	TrackingNone      TrackingMode = "none"
	TrackingLot       TrackingMode = "lot"
	TrackingLotExpiry TrackingMode = "lot_expiry"
)

// Valid reports whether m is a known tracking mode.
func (m TrackingMode) Valid() bool {
	return m == TrackingNone || m == TrackingLot || m == TrackingLotExpiry
}

// RequiresLot reports whether stock movements must name a lot.
func (m TrackingMode) RequiresLot() bool { return m == TrackingLot || m == TrackingLotExpiry }

// RequiresExpiry reports whether lots must have an expiry date.
func (m TrackingMode) RequiresExpiry() bool { return m == TrackingLotExpiry }

// Unit is a unit of measure for a product. Factor is how many base units
// one of this unit contains (a box of 12 pieces has Factor 12 when the base
// unit is pieces). The base unit itself has Factor 1.
type Unit struct {
	UOM    string `json:"uom"`
	Factor int64  `json:"factor"`
}

// Product is a stock keeping unit in the catalog. All stock quantities are
// stored in the base unit of measure as integers.
type Product struct {
	ID           int64        `json:"id"`
	SKU          string       `json:"sku"`
	Name         string       `json:"name"`
	BaseUOM      string       `json:"base_uom"`
	TrackingMode TrackingMode `json:"tracking_mode"`
	Units        []Unit       `json:"units"`
	CreatedAt    time.Time    `json:"created_at"`
}

// NormalizeUOM upper-cases and trims a unit code so "box " and "BOX" match.
func NormalizeUOM(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Validate checks the product fields and normalizes unit codes. It makes
// sure the base unit is present exactly once with factor 1.
func (p *Product) Validate() error {
	p.SKU = strings.TrimSpace(p.SKU)
	p.Name = strings.TrimSpace(p.Name)
	p.BaseUOM = NormalizeUOM(p.BaseUOM)
	switch {
	case p.SKU == "" || len(p.SKU) > 64:
		return Invalid(CodeValidation, "sku is required and must be at most 64 characters")
	case p.Name == "":
		return Invalid(CodeValidation, "name is required")
	case p.BaseUOM == "" || len(p.BaseUOM) > 16:
		return Invalid(CodeValidation, "base_uom is required and must be at most 16 characters")
	case p.TrackingMode == "":
		p.TrackingMode = TrackingNone
	case !p.TrackingMode.Valid():
		return Invalid(CodeValidation, "tracking_mode must be one of none, lot, lot_expiry")
	}
	seen := map[string]bool{p.BaseUOM: true}
	units := []Unit{{UOM: p.BaseUOM, Factor: 1}}
	for _, u := range p.Units {
		u.UOM = NormalizeUOM(u.UOM)
		if u.UOM == p.BaseUOM {
			if u.Factor != 1 {
				return Invalid(CodeValidation, "base unit %s must have factor 1", u.UOM)
			}
			continue
		}
		if err := ValidateUnit(u); err != nil {
			return err
		}
		if seen[u.UOM] {
			return Invalid(CodeValidation, "unit %s is listed twice", u.UOM)
		}
		seen[u.UOM] = true
		units = append(units, u)
	}
	p.Units = units
	return nil
}

// ValidateUnit checks a single unit definition.
func ValidateUnit(u Unit) error {
	if u.UOM == "" || len(u.UOM) > 16 {
		return Invalid(CodeValidation, "unit code is required and must be at most 16 characters")
	}
	if u.Factor < 1 {
		return Invalid(CodeValidation, "unit %s must have a factor of at least 1", u.UOM)
	}
	return nil
}

// Factor returns the base-unit factor for uom. An empty uom means the base
// unit.
func (p *Product) Factor(uom string) (int64, error) {
	uom = NormalizeUOM(uom)
	if uom == "" || uom == p.BaseUOM {
		return 1, nil
	}
	for _, u := range p.Units {
		if u.UOM == uom {
			return u.Factor, nil
		}
	}
	return 0, Invalid(CodeUnknownUnit, "product %s has no unit %s", p.SKU, uom).With("uom", uom)
}

// ToBase converts qty expressed in uom into base units. The quantity must be
// positive and the result must fit in an int64.
func (p *Product) ToBase(qty int64, uom string) (int64, error) {
	if qty <= 0 {
		return 0, Invalid(CodeValidation, "quantity must be positive")
	}
	f, err := p.Factor(uom)
	if err != nil {
		return 0, err
	}
	return MulQty(qty, f)
}

// MulQty multiplies two non-negative integers and reports overflow as a
// business error instead of silently wrapping around.
func MulQty(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, Invalid(CodeValidation, "negative value")
	}
	if a != 0 && b > math.MaxInt64/a {
		return 0, Invalid(CodeQuantityOverflow, "value %d x %d is too large", a, b)
	}
	return a * b, nil
}

// AddQty adds two non-negative integers with overflow detection.
func AddQty(a, b int64) (int64, error) {
	if a > math.MaxInt64-b {
		return 0, Invalid(CodeQuantityOverflow, "value %d + %d is too large", a, b)
	}
	return a + b, nil
}
