package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// CreateOrder inserts an order with its lines and sets the ids.
func (q *Queries) CreateOrder(ctx context.Context, o *domain.SalesOrder) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO sales_orders (order_number, warehouse_id, customer, status, backorder_of_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		o.OrderNumber, o.WarehouseID, o.Customer, o.Status, nullInt(o.BackorderOfID), o.CreatedAt, o.UpdatedAt)
	if IsDuplicate(err) {
		return domain.Conflict(domain.CodeDuplicate, "order number %s already exists", o.OrderNumber)
	}
	if err != nil {
		return err
	}
	if o.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for i := range o.Lines {
		l := &o.Lines[i]
		l.OrderID = o.ID
		res, err := q.db.ExecContext(ctx, `
			INSERT INTO order_lines (order_id, product_id, quantity, status) VALUES (?, ?, ?, ?)`,
			o.ID, l.ProductID, l.Quantity, l.Status)
		if err != nil {
			return err
		}
		if l.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	return nil
}

// GetOrder returns an order with its lines. With forUpdate the order row is
// locked, which serializes all state changes of one order.
func (q *Queries) GetOrder(ctx context.Context, id int64, forUpdate bool) (*domain.SalesOrder, error) {
	var (
		o  domain.SalesOrder
		bo sql.NullInt64
	)
	err := q.db.QueryRowContext(ctx, `
		SELECT id, order_number, warehouse_id, customer, status, backorder_of_id, created_at, updated_at
		FROM sales_orders WHERE id = ?`+lockSuffix(forUpdate), id).
		Scan(&o.ID, &o.OrderNumber, &o.WarehouseID, &o.Customer, &o.Status, &bo, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("order", id)
	}
	if err != nil {
		return nil, err
	}
	o.BackorderOfID = ptrInt(bo)
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, order_id, product_id, quantity, qty_allocated, qty_backordered, qty_picked, qty_shipped, status
		FROM order_lines WHERE order_id = ? ORDER BY id`+lockSuffix(forUpdate), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.OrderLine
		if err := rows.Scan(&l.ID, &l.OrderID, &l.ProductID, &l.Quantity, &l.QtyAllocated, &l.QtyBackordered,
			&l.QtyPicked, &l.QtyShipped, &l.Status); err != nil {
			return nil, err
		}
		o.Lines = append(o.Lines, l)
	}
	return &o, rows.Err()
}

// UpdateOrderStatus sets the order status.
func (q *Queries) UpdateOrderStatus(ctx context.Context, id int64, status domain.OrderStatus, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `UPDATE sales_orders SET status = ?, updated_at = ? WHERE id = ?`, status, now, id)
	return err
}

// UpdateOrderLine writes all mutable quantities and the status of a line.
func (q *Queries) UpdateOrderLine(ctx context.Context, l *domain.OrderLine) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE order_lines SET qty_allocated = ?, qty_backordered = ?, qty_picked = ?, qty_shipped = ?, status = ?
		WHERE id = ?`, l.QtyAllocated, l.QtyBackordered, l.QtyPicked, l.QtyShipped, l.Status, l.ID)
	return err
}

// InsertAllocation records a reservation and sets a.ID.
func (q *Queries) InsertAllocation(ctx context.Context, a *domain.Allocation, now time.Time) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO allocations (order_id, order_line_id, product_id, stock_item_id, location_id, lot_id, quantity,
			status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.OrderID, a.OrderLineID, a.ProductID, a.StockItemID, a.LocationID, nullInt(a.LotID), a.Quantity, a.Status, now, now)
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

// ListAllocations returns the allocations of an order, optionally only
// those in one of the given statuses, ordered by stock row id so that
// callers lock stock in a consistent order.
func (q *Queries) ListAllocations(ctx context.Context, orderID int64, statuses ...domain.AllocationStatus) ([]domain.Allocation, error) {
	query := `SELECT id, order_id, order_line_id, product_id, stock_item_id, location_id, lot_id, quantity, status
		FROM allocations WHERE order_id = ?`
	args := []any{orderID}
	if len(statuses) > 0 {
		query += " AND status IN ("
		for i, s := range statuses {
			if i > 0 {
				query += ","
			}
			query += "?"
			args = append(args, s)
		}
		query += ")"
	}
	rows, err := q.db.QueryContext(ctx, query+" ORDER BY stock_item_id, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Allocation{}
	for rows.Next() {
		var (
			a   domain.Allocation
			lot sql.NullInt64
		)
		if err := rows.Scan(&a.ID, &a.OrderID, &a.OrderLineID, &a.ProductID, &a.StockItemID, &a.LocationID, &lot,
			&a.Quantity, &a.Status); err != nil {
			return nil, err
		}
		a.LotID = ptrInt(lot)
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAllocation moves an allocation to another stock row and status.
func (q *Queries) UpdateAllocation(ctx context.Context, a *domain.Allocation, now time.Time) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE allocations SET stock_item_id = ?, location_id = ?, status = ?, updated_at = ? WHERE id = ?`,
		a.StockItemID, a.LocationID, a.Status, now, a.ID)
	return err
}

// PickList returns allocations of an order joined with location and lot
// details, in walking order (location code, then lot expiry).
func (q *Queries) PickList(ctx context.Context, orderID int64) ([]domain.PickTask, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT a.id, a.order_line_id, a.product_id, p.sku, a.location_id, loc.code, a.lot_id,
			COALESCE(lot.lot_number, ''), lot.expiry_date, a.quantity, a.status
		FROM allocations a
		JOIN products p ON p.id = a.product_id
		JOIN locations loc ON loc.id = a.location_id
		LEFT JOIN lots lot ON lot.id = a.lot_id
		WHERE a.order_id = ? AND a.status IN ('reserved', 'picked')
		ORDER BY loc.code, lot.expiry_date, a.id`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PickTask{}
	for rows.Next() {
		var (
			t   domain.PickTask
			lot sql.NullInt64
			exp sql.NullTime
		)
		if err := rows.Scan(&t.AllocationID, &t.OrderLineID, &t.ProductID, &t.SKU, &t.LocationID, &t.LocationCode,
			&lot, &t.LotNumber, &exp, &t.Quantity, &t.Status); err != nil {
			return nil, err
		}
		t.LotID, t.ExpiryDate = ptrInt(lot), ptrDate(exp)
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateShipment inserts a shipment header and sets s.ID.
func (q *Queries) CreateShipment(ctx context.Context, s *domain.Shipment) error {
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO shipments (order_id, total_cogs, shipped_at) VALUES (?, 0, ?)`, s.OrderID, s.ShippedAt)
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	return err
}

// AddShipmentLine inserts a shipment line and sets l.ID.
func (q *Queries) AddShipmentLine(ctx context.Context, shipmentID int64, l *domain.ShipmentLine) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO shipment_lines (shipment_id, order_line_id, product_id, lot_id, quantity, cogs)
		VALUES (?, ?, ?, ?, ?, ?)`, shipmentID, l.OrderLineID, l.ProductID, nullInt(l.LotID), l.Quantity, l.COGS)
	if err != nil {
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// SetShipmentCOGS stores the total cost of goods sold of a shipment.
func (q *Queries) SetShipmentCOGS(ctx context.Context, id, total int64) error {
	_, err := q.db.ExecContext(ctx, `UPDATE shipments SET total_cogs = ? WHERE id = ?`, total, id)
	return err
}

// GetShipmentByOrder returns the shipment of an order, or nil if the order
// has not shipped.
func (q *Queries) GetShipmentByOrder(ctx context.Context, orderID int64) (*domain.Shipment, error) {
	var s domain.Shipment
	err := q.db.QueryRowContext(ctx,
		`SELECT id, order_id, total_cogs, shipped_at FROM shipments WHERE order_id = ?`, orderID).
		Scan(&s.ID, &s.OrderID, &s.TotalCOGS, &s.ShippedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, order_line_id, product_id, lot_id, quantity, cogs FROM shipment_lines
		WHERE shipment_id = ? ORDER BY id`, s.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			l   domain.ShipmentLine
			lot sql.NullInt64
		)
		if err := rows.Scan(&l.ID, &l.OrderLineID, &l.ProductID, &lot, &l.Quantity, &l.COGS); err != nil {
			return nil, err
		}
		l.LotID = ptrInt(lot)
		s.Lines = append(s.Lines, l)
	}
	return &s, rows.Err()
}
