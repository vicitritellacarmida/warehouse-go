package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// InsertCostLayer adds a FIFO cost layer and sets l.ID.
func (q *Queries) InsertCostLayer(ctx context.Context, l *domain.CostLayer) error {
	res, err := q.db.ExecContext(ctx, `
		INSERT INTO cost_layers (product_id, warehouse_id, unit_cost, qty_received, qty_remaining, source_type,
			source_id, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ProductID, l.WarehouseID, l.UnitCost, l.QtyReceived, l.QtyRemaining, l.SourceType, l.SourceID, l.ReceivedAt)
	if err != nil {
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// LockOpenCostLayers locks and returns the layers of a product in a
// warehouse that still hold stock, oldest first.
func (q *Queries) LockOpenCostLayers(ctx context.Context, productID, warehouseID int64) ([]domain.CostLayer, error) {
	rows, err := q.db.QueryContext(ctx, `
		SELECT id, product_id, warehouse_id, unit_cost, qty_received, qty_remaining, source_type, source_id, received_at
		FROM cost_layers WHERE product_id = ? AND warehouse_id = ? AND qty_remaining > 0
		ORDER BY received_at, id FOR UPDATE`, productID, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CostLayer
	for rows.Next() {
		var l domain.CostLayer
		if err := rows.Scan(&l.ID, &l.ProductID, &l.WarehouseID, &l.UnitCost, &l.QtyReceived, &l.QtyRemaining,
			&l.SourceType, &l.SourceID, &l.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ConsumeCostLayer takes qty from a layer and records the consumption.
func (q *Queries) ConsumeCostLayer(ctx context.Context, c domain.Consumption, refType string, refID int64, now time.Time) error {
	res, err := q.db.ExecContext(ctx,
		`UPDATE cost_layers SET qty_remaining = qty_remaining - ? WHERE id = ? AND qty_remaining >= ?`,
		c.Quantity, c.LayerID, c.Quantity)
	if err != nil {
		return err
	}
	if rowsAffected(res) != 1 {
		return domain.Conflict(domain.CodeInsufficientCost, "cost layer %d has too little quantity", c.LayerID)
	}
	_, err = q.db.ExecContext(ctx, `
		INSERT INTO cost_consumptions (layer_id, quantity, unit_cost, reference_type, reference_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, c.LayerID, c.Quantity, c.UnitCost, refType, refID, now)
	return err
}

// LatestUnitCost returns the unit cost of the newest layer of a product in
// a warehouse, or ok=false when there is none.
func (q *Queries) LatestUnitCost(ctx context.Context, productID, warehouseID int64) (cost int64, ok bool, err error) {
	err = q.db.QueryRowContext(ctx, `
		SELECT unit_cost FROM cost_layers WHERE product_id = ? AND warehouse_id = ?
		ORDER BY received_at DESC, id DESC LIMIT 1`, productID, warehouseID).Scan(&cost)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return cost, err == nil, err
}

// Valuation returns quantity and FIFO value per product and warehouse.
// warehouseID 0 means all warehouses.
func (q *Queries) Valuation(ctx context.Context, warehouseID int64) ([]domain.ValuationRow, error) {
	query := `SELECT c.product_id, p.sku, c.warehouse_id, SUM(c.qty_remaining), SUM(c.qty_remaining * c.unit_cost)
		FROM cost_layers c JOIN products p ON p.id = c.product_id WHERE c.qty_remaining > 0`
	var args []any
	if warehouseID > 0 {
		query += " AND c.warehouse_id = ?"
		args = append(args, warehouseID)
	}
	query += " GROUP BY c.product_id, p.sku, c.warehouse_id ORDER BY c.warehouse_id, p.sku"
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ValuationRow{}
	for rows.Next() {
		var r domain.ValuationRow
		if err := rows.Scan(&r.ProductID, &r.SKU, &r.WarehouseID, &r.Quantity, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
