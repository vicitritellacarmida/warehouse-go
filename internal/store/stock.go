package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// GetOrCreateLot returns the lot with this number for the product, creating
// it if needed. An existing lot with a different expiry date is a conflict.
func (q *Queries) GetOrCreateLot(ctx context.Context, productID int64, lotNumber string, expiry *domain.Date, now time.Time) (*domain.Lot, error) {
	if _, err := q.db.ExecContext(ctx, `
		INSERT INTO lots (product_id, lot_number, expiry_date, created_at) VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = id`,
		productID, lotNumber, nullDate(expiry), now); err != nil {
		return nil, err
	}
	var (
		lot domain.Lot
		exp sql.NullTime
	)
	err := q.db.QueryRowContext(ctx,
		`SELECT id, product_id, lot_number, expiry_date, created_at FROM lots WHERE product_id = ? AND lot_number = ?`,
		productID, lotNumber).Scan(&lot.ID, &lot.ProductID, &lot.LotNumber, &exp, &lot.CreatedAt)
	if err != nil {
		return nil, err
	}
	lot.ExpiryDate = ptrDate(exp)
	if (lot.ExpiryDate == nil) != (expiry == nil) || (expiry != nil && !lot.ExpiryDate.Equal(*expiry)) {
		return nil, domain.Conflict(domain.CodeLotExpiryMismatch,
			"lot %s already exists with a different expiry date", lotNumber).With("lot_id", lot.ID)
	}
	return &lot, nil
}

// GetLot returns one lot.
func (q *Queries) GetLot(ctx context.Context, id int64) (*domain.Lot, error) {
	var (
		lot domain.Lot
		exp sql.NullTime
	)
	err := q.db.QueryRowContext(ctx,
		`SELECT id, product_id, lot_number, expiry_date, created_at FROM lots WHERE id = ?`, id).
		Scan(&lot.ID, &lot.ProductID, &lot.LotNumber, &exp, &lot.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("lot", id)
	}
	lot.ExpiryDate = ptrDate(exp)
	return &lot, err
}

const stockCols = `si.id, si.product_id, si.warehouse_id, si.location_id, loc.location_type, si.lot_id,
	lot.expiry_date, si.on_hand, si.reserved, si.received_at`

const stockFrom = ` FROM stock_items si
	JOIN locations loc ON loc.id = si.location_id
	LEFT JOIN lots lot ON lot.id = si.lot_id`

func scanStock(sc interface{ Scan(...any) error }) (*domain.StockItem, error) {
	var (
		s     domain.StockItem
		lotID sql.NullInt64
		exp   sql.NullTime
	)
	if err := sc.Scan(&s.ID, &s.ProductID, &s.WarehouseID, &s.LocationID, &s.LocationType, &lotID,
		&exp, &s.OnHand, &s.Reserved, &s.ReceivedAt); err != nil {
		return nil, err
	}
	s.LotID, s.ExpiryDate = ptrInt(lotID), ptrDate(exp)
	return &s, nil
}

func (q *Queries) queryStock(ctx context.Context, query string, args ...any) ([]*domain.StockItem, error) {
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.StockItem
	for rows.Next() {
		s, err := scanStock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// EnsureStockItem returns the id of the stock row for product + location +
// lot, creating an empty row if none exists. When the row exists its
// received_at is moved back to receivedAt if that is older, so FIFO keeps
// seeing the oldest units in the row.
func (q *Queries) EnsureStockItem(ctx context.Context, productID, warehouseID, locationID int64, lotID *int64, receivedAt time.Time) (int64, error) {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO stock_items (product_id, warehouse_id, location_id, lot_id, on_hand, reserved, received_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?) AS new
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(stock_items.id),
			received_at = IF(stock_items.on_hand = 0, new.received_at, LEAST(stock_items.received_at, new.received_at))`,
		productID, warehouseID, locationID, nullInt(lotID), receivedAt, receivedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FindStockItemForUpdate locks and returns the stock row for product +
// location + lot, or nil if it does not exist.
func (q *Queries) FindStockItemForUpdate(ctx context.Context, productID, locationID int64, lotID *int64) (*domain.StockItem, error) {
	var lotKey int64
	if lotID != nil {
		lotKey = *lotID
	}
	s, err := scanStock(q.db.QueryRowContext(ctx, `SELECT `+stockCols+stockFrom+`
		WHERE si.location_id = ? AND si.product_id = ? AND si.lot_key = ? FOR UPDATE OF si`,
		locationID, productID, lotKey))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// LockStockItems locks the given stock rows in id order.
func (q *Queries) LockStockItems(ctx context.Context, ids []int64) (map[int64]*domain.StockItem, error) {
	out := map[int64]*domain.StockItem{}
	if len(ids) == 0 {
		return out, nil
	}
	in, args := inClause(ids)
	items, err := q.queryStock(ctx, `SELECT `+stockCols+stockFrom+`
		WHERE si.id IN (`+in+`) ORDER BY si.id FOR UPDATE OF si`, args...)
	if err != nil {
		return nil, err
	}
	for _, s := range items {
		out[s.ID] = s
	}
	return out, nil
}

// LockAllocatableStock locks, in id order, every stock row of the given
// products in the warehouse that could be allocated: in a storage or
// picking location that is not being counted, with free quantity. Locking
// in one statement ordered by id is what makes concurrent allocations
// queue up instead of deadlocking or overselling.
func (q *Queries) LockAllocatableStock(ctx context.Context, warehouseID int64, productIDs []int64) ([]*domain.StockItem, error) {
	if len(productIDs) == 0 {
		return nil, nil
	}
	in, args := inClause(productIDs)
	args = append([]any{warehouseID}, args...)
	return q.queryStock(ctx, `SELECT `+stockCols+stockFrom+`
		WHERE si.warehouse_id = ? AND si.product_id IN (`+in+`)
		  AND loc.location_type IN ('storage', 'picking') AND loc.open_count_id IS NULL
		  AND si.on_hand > si.reserved
		ORDER BY si.id FOR UPDATE OF si`, args...)
}

// StockAtLocation locks and returns all non-empty stock rows in a location.
func (q *Queries) StockAtLocation(ctx context.Context, locationID int64) ([]*domain.StockItem, error) {
	return q.queryStock(ctx, `SELECT `+stockCols+stockFrom+`
		WHERE si.location_id = ? AND si.on_hand > 0 ORDER BY si.id FOR UPDATE OF si`, locationID)
}

// UpdateStock changes on_hand and reserved of a row by the given deltas.
// The update is guarded so that on_hand, reserved and on_hand - reserved
// never go negative; if the guard fails the stock is insufficient.
func (q *Queries) UpdateStock(ctx context.Context, id, dOnHand, dReserved int64, now time.Time) error {
	if dOnHand == 0 && dReserved == 0 {
		return nil
	}
	res, err := q.db.ExecContext(ctx, `
		UPDATE stock_items SET on_hand = on_hand + ?, reserved = reserved + ?, updated_at = ?
		WHERE id = ? AND reserved + ? >= 0 AND on_hand + ? >= reserved + ?`,
		dOnHand, dReserved, now, id, dReserved, dOnHand, dReserved)
	if isCheckViolation(err) {
		return domain.Conflict(domain.CodeInsufficientStock, "stock row %d has too little stock", id)
	}
	if err != nil {
		return err
	}
	if rowsAffected(res) != 1 {
		return domain.Conflict(domain.CodeInsufficientStock, "stock row %d has too little free stock", id).
			With("stock_item_id", id)
	}
	return nil
}

// InsertMovement appends a movement to the ledger and sets m.ID.
func (q *Queries) InsertMovement(ctx context.Context, m *domain.Movement) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO stock_movements (movement_type, product_id, warehouse_id, lot_id, from_location_id,
			to_location_id, quantity, reference_type, reference_id, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Type, m.ProductID, m.WarehouseID, nullInt(m.LotID), nullInt(m.FromLocationID), nullInt(m.ToLocationID),
		m.Quantity, m.ReferenceType, m.ReferenceID, m.Note, m.CreatedAt)
	if err != nil {
		return err
	}
	m.ID, err = res.LastInsertId()
	return err
}

// MovementFilter narrows the movement history. Zero values mean "any".
type MovementFilter struct {
	ProductID     int64
	WarehouseID   int64
	LocationID    int64
	Type          domain.MovementType
	ReferenceType string
	ReferenceID   int64
	BeforeID      int64 // cursor: only movements with a smaller id
	Limit         int
}

// ListMovements returns movements newest first.
func (q *Queries) ListMovements(ctx context.Context, f MovementFilter) ([]domain.Movement, error) {
	query := `SELECT id, movement_type, product_id, warehouse_id, lot_id, from_location_id, to_location_id,
		quantity, reference_type, reference_id, note, created_at FROM stock_movements WHERE 1 = 1`
	var args []any
	add := func(cond string, v any) {
		query += " AND " + cond
		args = append(args, v)
	}
	if f.ProductID > 0 {
		add("product_id = ?", f.ProductID)
	}
	if f.WarehouseID > 0 {
		add("warehouse_id = ?", f.WarehouseID)
	}
	if f.LocationID > 0 {
		query += " AND (from_location_id = ? OR to_location_id = ?)"
		args = append(args, f.LocationID, f.LocationID)
	}
	if f.Type != "" {
		add("movement_type = ?", f.Type)
	}
	if f.ReferenceType != "" {
		add("reference_type = ?", f.ReferenceType)
	}
	if f.ReferenceID > 0 {
		add("reference_id = ?", f.ReferenceID)
	}
	if f.BeforeID > 0 {
		add("id < ?", f.BeforeID)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, f.Limit)

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Movement{}
	for rows.Next() {
		var (
			m              domain.Movement
			lot, from, too sql.NullInt64
		)
		if err := rows.Scan(&m.ID, &m.Type, &m.ProductID, &m.WarehouseID, &lot, &from, &too, &m.Quantity,
			&m.ReferenceType, &m.ReferenceID, &m.Note, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.LotID, m.FromLocationID, m.ToLocationID = ptrInt(lot), ptrInt(from), ptrInt(too)
		out = append(out, m)
	}
	return out, rows.Err()
}
