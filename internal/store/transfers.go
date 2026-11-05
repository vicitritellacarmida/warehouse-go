package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// Transfer line part kinds.
const (
	partLot  = "lot"
	partCost = "cost"
)

// CreateTransfer inserts a transfer with its lines and sets the ids.
func (q *Queries) CreateTransfer(ctx context.Context, t *domain.Transfer) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO transfers (from_warehouse_id, to_warehouse_id, status, created_at) VALUES (?, ?, ?, ?)`,
		t.FromWarehouseID, t.ToWarehouseID, t.Status, t.CreatedAt)
	if err != nil {
		return err
	}
	if t.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for i := range t.Lines {
		l := &t.Lines[i]
		l.TransferID = t.ID
		res, err := q.db.ExecContext(ctx,
			`INSERT INTO transfer_lines (transfer_id, product_id, quantity) VALUES (?, ?, ?)`, t.ID, l.ProductID, l.Quantity)
		if err != nil {
			return err
		}
		if l.ID, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	return nil
}

// GetTransfer returns a transfer with lines and shipped parts.
func (q *Queries) GetTransfer(ctx context.Context, id int64, forUpdate bool) (*domain.Transfer, error) {
	var (
		t                 domain.Transfer
		shipped, received sql.NullTime
	)
	err := q.db.QueryRowContext(ctx, `
		SELECT id, from_warehouse_id, to_warehouse_id, status, created_at, shipped_at, received_at
		FROM transfers WHERE id = ?`+lockSuffix(forUpdate), id).
		Scan(&t.ID, &t.FromWarehouseID, &t.ToWarehouseID, &t.Status, &t.CreatedAt, &shipped, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("transfer", id)
	}
	if err != nil {
		return nil, err
	}
	t.ShippedAt, t.ReceivedAt = ptrTime(shipped), ptrTime(received)

	rows, err := q.db.QueryContext(ctx, `
		SELECT id, transfer_id, product_id, quantity, qty_shipped, qty_received
		FROM transfer_lines WHERE transfer_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	index := map[int64]int{}
	for rows.Next() {
		l := domain.TransferLine{Lots: []domain.TransferLinePart{}, Costs: []domain.TransferLinePart{}}
		if err := rows.Scan(&l.ID, &l.TransferID, &l.ProductID, &l.Quantity, &l.QtyShipped, &l.QtyReceived); err != nil {
			return nil, err
		}
		index[l.ID] = len(t.Lines)
		t.Lines = append(t.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	parts, err := q.db.QueryContext(ctx, `
		SELECT p.id, p.transfer_line_id, p.part_kind, p.lot_id, p.unit_cost, p.received_at, p.quantity, p.qty_received
		FROM transfer_line_parts p JOIN transfer_lines l ON l.id = p.transfer_line_id
		WHERE l.transfer_id = ? ORDER BY p.id`, id)
	if err != nil {
		return nil, err
	}
	defer parts.Close()
	for parts.Next() {
		var (
			p      domain.TransferLinePart
			lineID int64
			kind   string
			lot    sql.NullInt64
		)
		if err := parts.Scan(&p.ID, &lineID, &kind, &lot, &p.UnitCost, &p.ReceivedAt, &p.Quantity, &p.QtyReceived); err != nil {
			return nil, err
		}
		p.LotID = ptrInt(lot)
		l := &t.Lines[index[lineID]]
		if kind == partLot {
			l.Lots = append(l.Lots, p)
		} else {
			l.Costs = append(l.Costs, p)
		}
	}
	if err := parts.Err(); err != nil {
		return nil, err
	}
	if t.Status == domain.TransferReceived || t.Status == domain.TransferDiscrepancy {
		for i := range t.Lines {
			t.Lines[i].ComputeDiscrepancy()
		}
	}
	return &t, nil
}

// AddTransferLot records a shipped lot slice of a transfer line.
func (q *Queries) AddTransferLot(ctx context.Context, lineID int64, lotID *int64, receivedAt time.Time, qty int64) error {
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO transfer_line_parts (transfer_line_id, part_kind, lot_id, received_at, quantity)
		VALUES (?, ?, ?, ?, ?)`, lineID, partLot, nullInt(lotID), receivedAt, qty)
	return err
}

// AddTransferCost records a shipped cost slice of a transfer line.
func (q *Queries) AddTransferCost(ctx context.Context, lineID, unitCost, qty int64, now time.Time) error {
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO transfer_line_parts (transfer_line_id, part_kind, unit_cost, received_at, quantity)
		VALUES (?, ?, ?, ?, ?)`, lineID, partCost, unitCost, now, qty)
	return err
}

// SetTransferPartReceived stores the received quantity of a part.
func (q *Queries) SetTransferPartReceived(ctx context.Context, partID, qty int64) error {
	_, err := q.db.ExecContext(ctx, `UPDATE transfer_line_parts SET qty_received = ? WHERE id = ?`, qty, partID)
	return err
}

// SetTransferLineQty stores shipped and received quantities of a line.
func (q *Queries) SetTransferLineQty(ctx context.Context, lineID, shipped, received int64) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE transfer_lines SET qty_shipped = ?, qty_received = ? WHERE id = ?`, shipped, received, lineID)
	return err
}

// SetTransferStatus updates the status and the shipped/received times.
func (q *Queries) SetTransferStatus(ctx context.Context, t *domain.Transfer) error {
	toNull := func(p *time.Time) sql.NullTime {
		if p == nil {
			return sql.NullTime{}
		}
		return sql.NullTime{Time: *p, Valid: true}
	}
	_, err := q.db.ExecContext(ctx, `UPDATE transfers SET status = ?, shipped_at = ?, received_at = ? WHERE id = ?`,
		t.Status, toNull(t.ShippedAt), toNull(t.ReceivedAt), t.ID)
	return err
}
