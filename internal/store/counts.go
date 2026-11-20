package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// CreateCount inserts a cycle count header and its snapshot lines.
func (q *Queries) CreateCount(ctx context.Context, c *domain.CycleCount) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO cycle_counts (location_id, warehouse_id, status, created_at) VALUES (?, ?, ?, ?)`,
		c.LocationID, c.WarehouseID, c.Status, c.CreatedAt)
	if err != nil {
		return err
	}
	if c.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for i := range c.Lines {
		if err := q.UpsertCountLine(ctx, c.ID, &c.Lines[i]); err != nil {
			return err
		}
	}
	return nil
}

// UpsertCountLine inserts a count line or, if one exists for the same
// product and lot, updates its counted quantity.
func (q *Queries) UpsertCountLine(ctx context.Context, countID int64, l *domain.CountLine) error {
	l.CountID = countID
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO cycle_count_lines (count_id, product_id, lot_id, system_qty, counted_qty)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(cycle_count_lines.id), counted_qty = ?`,
		countID, l.ProductID, nullInt(l.LotID), l.SystemQty, nullInt(l.CountedQty), nullInt(l.CountedQty))
	if err != nil {
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// GetCount returns a cycle count with its lines, optionally locking it.
func (q *Queries) GetCount(ctx context.Context, id int64, forUpdate bool) (*domain.CycleCount, error) {
	var (
		c      domain.CycleCount
		posted sql.NullTime
	)
	err := q.db.QueryRowContext(ctx, `
		SELECT id, location_id, warehouse_id, status, reason, note, created_at, posted_at
		FROM cycle_counts WHERE id = ?`+lockSuffix(forUpdate), id).
		Scan(&c.ID, &c.LocationID, &c.WarehouseID, &c.Status, &c.Reason, &c.Note, &c.CreatedAt, &posted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("cycle_count", id)
	}
	if err != nil {
		return nil, err
	}
	c.PostedAt = ptrTime(posted)
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, count_id, product_id, lot_id, system_qty, counted_qty
		FROM cycle_count_lines WHERE count_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			l            domain.CountLine
			lot, counted sql.NullInt64
		)
		if err := rows.Scan(&l.ID, &l.CountID, &l.ProductID, &lot, &l.SystemQty, &counted); err != nil {
			return nil, err
		}
		l.LotID, l.CountedQty = ptrInt(lot), ptrInt(counted)
		l.ComputeVariance()
		c.Lines = append(c.Lines, l)
	}
	return &c, rows.Err()
}

// FinishCount stores the final status, reason and note of a count.
func (q *Queries) FinishCount(ctx context.Context, c *domain.CycleCount, at time.Time) error {
	var posted sql.NullTime
	if c.Status == domain.CountPosted {
		posted = sql.NullTime{Time: at, Valid: true}
	}
	_, err := q.db.ExecContext(ctx,
		`UPDATE cycle_counts SET status = ?, reason = ?, note = ?, posted_at = ? WHERE id = ?`,
		c.Status, c.Reason, c.Note, posted, c.ID)
	return err
}
