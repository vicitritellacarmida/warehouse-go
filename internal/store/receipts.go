package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// CreateReceipt inserts a receipt with its lines and sets the ids.
func (q *Queries) CreateReceipt(ctx context.Context, r *domain.Receipt) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO receipts (warehouse_id, reference, supplier, status, tolerance_bp, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, r.WarehouseID, r.Reference, r.Supplier, r.Status, r.ToleranceBP, r.CreatedAt)
	if IsDuplicate(err) {
		return domain.Conflict(domain.CodeDuplicate, "receipt %s already exists in warehouse %d", r.Reference, r.WarehouseID)
	}
	if err != nil {
		return err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for i := range r.Lines {
		l := &r.Lines[i]
		l.ReceiptID = r.ID
		res, err := q.db.ExecContext(ctx, `
			INSERT INTO receipt_lines (receipt_id, product_id, expected_qty, unit_cost) VALUES (?, ?, ?, ?)`,
			r.ID, l.ProductID, l.ExpectedQty, l.UnitCost)
		if IsDuplicate(err) {
			return domain.Invalid(domain.CodeValidation, "product %d appears on more than one line", l.ProductID)
		}
		if err != nil {
			return err
		}
		if l.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	return nil
}

// GetReceipt returns a receipt with its lines, optionally locking it.
func (q *Queries) GetReceipt(ctx context.Context, id int64, forUpdate bool) (*domain.Receipt, error) {
	var (
		r      domain.Receipt
		closed sql.NullTime
	)
	err := q.db.QueryRowContext(ctx, `
		SELECT id, warehouse_id, reference, supplier, status, tolerance_bp, created_at, closed_at
		FROM receipts WHERE id = ?`+lockSuffix(forUpdate), id).
		Scan(&r.ID, &r.WarehouseID, &r.Reference, &r.Supplier, &r.Status, &r.ToleranceBP, &r.CreatedAt, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("receipt", id)
	}
	if err != nil {
		return nil, err
	}
	r.ClosedAt = ptrTime(closed)
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, receipt_id, product_id, expected_qty, received_qty, unit_cost
		FROM receipt_lines WHERE receipt_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.ReceiptLine
		if err := rows.Scan(&l.ID, &l.ReceiptID, &l.ProductID, &l.ExpectedQty, &l.ReceivedQty, &l.UnitCost); err != nil {
			return nil, err
		}
		l.Outcome = domain.LineOutcome(l, r.ToleranceBP)
		r.Lines = append(r.Lines, l)
	}
	return &r, rows.Err()
}

// SetReceiptStatus updates the status and, for closed receipts, closed_at.
func (q *Queries) SetReceiptStatus(ctx context.Context, id int64, status domain.ReceiptStatus, closedAt *time.Time) error {
	var c sql.NullTime
	if closedAt != nil {
		c = sql.NullTime{Time: *closedAt, Valid: true}
	}
	_, err := q.db.ExecContext(ctx, `UPDATE receipts SET status = ?, closed_at = ? WHERE id = ?`, status, c, id)
	return err
}

// SetReceiptLineReceived stores the received quantity of a line.
func (q *Queries) SetReceiptLineReceived(ctx context.Context, lineID, received int64) error {
	_, err := q.db.ExecContext(ctx, `UPDATE receipt_lines SET received_qty = ? WHERE id = ?`, received, lineID)
	return err
}
