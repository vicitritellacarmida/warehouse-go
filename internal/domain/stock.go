package domain

import "time"

// MovementType classifies an entry in the stock movement ledger.
type MovementType string

// Movement types. Every change to on-hand stock is one of these.
const (
	MoveReceipt     MovementType = "receipt"      // supplier -> receiving location
	MovePutaway     MovementType = "putaway"      // receiving -> storage/picking/quarantine
	MoveInternal    MovementType = "move"         // between internal locations
	MovePick        MovementType = "pick"         // storage/picking -> shipping (stays reserved)
	MoveShip        MovementType = "ship"         // shipping -> customer
	MoveAdjustment  MovementType = "adjustment"   // cycle count variance
	MoveTransferOut MovementType = "transfer_out" // source location -> in transit
	MoveTransferIn  MovementType = "transfer_in"  // in transit -> destination location
)

// Lot is a batch of a product, identified by a lot number and optionally
// carrying an expiry date.
type Lot struct {
	ID         int64     `json:"id"`
	ProductID  int64     `json:"product_id"`
	LotNumber  string    `json:"lot_number"`
	ExpiryDate *Date     `json:"expiry_date,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// StockItem is the quantity of one product in one location and one lot.
// OnHand is physically present; Reserved is promised to orders or picks.
// ReceivedAt is the oldest receipt time of the units in this row and drives
// FIFO allocation.
type StockItem struct {
	ID           int64        `json:"id"`
	ProductID    int64        `json:"product_id"`
	WarehouseID  int64        `json:"warehouse_id"`
	LocationID   int64        `json:"location_id"`
	LocationType LocationType `json:"location_type"`
	LotID        *int64       `json:"lot_id,omitempty"`
	ExpiryDate   *Date        `json:"expiry_date,omitempty"`
	OnHand       int64        `json:"on_hand"`
	Reserved     int64        `json:"reserved"`
	ReceivedAt   time.Time    `json:"received_at"`
}

// Available is on hand minus reserved. Quarantine stock is never available.
func (s StockItem) Available() int64 {
	if !s.LocationType.CountsAsAvailable() {
		return 0
	}
	return s.OnHand - s.Reserved
}

// Movement is one append-only entry in the stock ledger. A movement with a
// FromLocationID takes stock out of that location; one with a ToLocationID
// puts stock into it. Both are set for internal moves.
type Movement struct {
	ID             int64        `json:"id"`
	Type           MovementType `json:"movement_type"`
	ProductID      int64        `json:"product_id"`
	WarehouseID    int64        `json:"warehouse_id"`
	LotID          *int64       `json:"lot_id,omitempty"`
	FromLocationID *int64       `json:"from_location_id,omitempty"`
	ToLocationID   *int64       `json:"to_location_id,omitempty"`
	Quantity       int64        `json:"quantity"`
	ReferenceType  string       `json:"reference_type"`
	ReferenceID    int64        `json:"reference_id"`
	Note           string       `json:"note,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

// ValidateLotInput checks a lot number and expiry date against the product's
// tracking mode.
func ValidateLotInput(p *Product, lotNumber string, expiry *Date) error {
	switch {
	case p.TrackingMode.RequiresLot() && lotNumber == "":
		return Invalid(CodeLotRequired, "product %s is lot tracked; lot_number is required", p.SKU)
	case !p.TrackingMode.RequiresLot() && (lotNumber != "" || expiry != nil):
		return Invalid(CodeLotNotAllowed, "product %s is not lot tracked", p.SKU)
	case p.TrackingMode.RequiresExpiry() && expiry == nil:
		return Invalid(CodeExpiryRequired, "product %s requires an expiry_date", p.SKU)
	case p.TrackingMode == TrackingLot && expiry != nil:
		return Invalid(CodeLotNotAllowed, "product %s lots have no expiry date", p.SKU)
	case len(lotNumber) > 64:
		return Invalid(CodeValidation, "lot_number must be at most 64 characters")
	}
	return nil
}
