package store

import (
	"context"
	"database/sql"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// StockFilter narrows the stock report. Zero values mean "any".
type StockFilter struct {
	ProductID   int64
	WarehouseID int64
	LocationID  int64
	GroupBy     domain.StockGrouping
}

// StockReport sums stock rows at the requested level of detail.
func (q *Queries) StockReport(ctx context.Context, f StockFilter) ([]domain.StockRow, error) {
	var cols, group string
	switch f.GroupBy {
	case domain.GroupByProduct:
		cols = "si.product_id, p.sku, NULL, NULL, '', '', NULL, '', NULL"
		group = "si.product_id, p.sku"
	case domain.GroupByWarehouse:
		cols = "si.product_id, p.sku, si.warehouse_id, NULL, '', '', NULL, '', NULL"
		group = "si.product_id, p.sku, si.warehouse_id"
	case domain.GroupByLot:
		cols = "si.product_id, p.sku, si.warehouse_id, NULL, '', '', si.lot_id, COALESCE(MAX(lot.lot_number), ''), MAX(lot.expiry_date)"
		group = "si.product_id, p.sku, si.warehouse_id, si.lot_id"
	default:
		cols = "si.product_id, p.sku, si.warehouse_id, si.location_id, loc.code, loc.location_type, si.lot_id, " +
			"COALESCE(MAX(lot.lot_number), ''), MAX(lot.expiry_date)"
		group = "si.product_id, p.sku, si.warehouse_id, si.location_id, loc.code, loc.location_type, si.lot_id"
	}
	query := `SELECT ` + cols + `,
			SUM(si.on_hand), SUM(si.reserved),
			SUM(CASE WHEN loc.location_type <> 'quarantine' THEN si.on_hand - si.reserved ELSE 0 END),
			SUM(CASE WHEN loc.location_type = 'quarantine' THEN si.on_hand ELSE 0 END)
		FROM stock_items si
		JOIN products p ON p.id = si.product_id
		JOIN locations loc ON loc.id = si.location_id
		LEFT JOIN lots lot ON lot.id = si.lot_id
		WHERE 1 = 1`
	var args []any
	if f.ProductID > 0 {
		query += " AND si.product_id = ?"
		args = append(args, f.ProductID)
	}
	if f.WarehouseID > 0 {
		query += " AND si.warehouse_id = ?"
		args = append(args, f.WarehouseID)
	}
	if f.LocationID > 0 {
		query += " AND si.location_id = ?"
		args = append(args, f.LocationID)
	}
	query += " GROUP BY " + group + " HAVING SUM(si.on_hand) > 0 ORDER BY " + group

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.StockRow{}
	for rows.Next() {
		var (
			r              domain.StockRow
			wh, loc, lotID sql.NullInt64
			exp            sql.NullTime
		)
		if err := rows.Scan(&r.ProductID, &r.SKU, &wh, &loc, &r.LocationCode, &r.LocationType, &lotID, &r.LotNumber,
			&exp, &r.OnHand, &r.Reserved, &r.Available, &r.Quarantined); err != nil {
			return nil, err
		}
		r.WarehouseID, r.LocationID, r.LotID, r.ExpiryDate = ptrInt(wh), ptrInt(loc), ptrInt(lotID), ptrDate(exp)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ExpiringStock returns on-hand stock in lots that expire on or before the
// given date, soonest first.
func (q *Queries) ExpiringStock(ctx context.Context, warehouseID int64, before, today domain.Date) ([]domain.ExpiringRow, error) {
	query := `SELECT si.product_id, p.sku, si.warehouse_id, si.location_id, loc.code, lot.id, lot.lot_number,
			lot.expiry_date, si.on_hand
		FROM stock_items si
		JOIN products p ON p.id = si.product_id
		JOIN locations loc ON loc.id = si.location_id
		JOIN lots lot ON lot.id = si.lot_id
		WHERE si.on_hand > 0 AND lot.expiry_date IS NOT NULL AND lot.expiry_date <= ?`
	args := []any{before.Time()}
	if warehouseID > 0 {
		query += " AND si.warehouse_id = ?"
		args = append(args, warehouseID)
	}
	query += " ORDER BY lot.expiry_date, p.sku, loc.code"
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ExpiringRow{}
	for rows.Next() {
		var (
			r   domain.ExpiringRow
			exp sql.NullTime
		)
		if err := rows.Scan(&r.ProductID, &r.SKU, &r.WarehouseID, &r.LocationID, &r.LocationCode, &r.LotID,
			&r.LotNumber, &exp, &r.OnHand); err != nil {
			return nil, err
		}
		r.ExpiryDate = domain.DateOf(exp.Time)
		r.DaysLeft = today.DaysUntil(r.ExpiryDate)
		r.Expired = r.ExpiryDate.Before(today)
		out = append(out, r)
	}
	return out, rows.Err()
}

// LowStock returns products whose available quantity in a warehouse is at
// or below their reorder point. warehouseID 0 means all warehouses.
func (q *Queries) LowStock(ctx context.Context, warehouseID int64) ([]domain.LowStockRow, error) {
	query := `SELECT rp.product_id, p.sku, rp.warehouse_id, rp.reorder_point, COALESCE(s.available, 0)
		FROM reorder_points rp
		JOIN products p ON p.id = rp.product_id
		LEFT JOIN (
			SELECT si.product_id, si.warehouse_id, SUM(si.on_hand - si.reserved) AS available
			FROM stock_items si JOIN locations loc ON loc.id = si.location_id
			WHERE loc.location_type <> 'quarantine'
			GROUP BY si.product_id, si.warehouse_id
		) s ON s.product_id = rp.product_id AND s.warehouse_id = rp.warehouse_id
		WHERE COALESCE(s.available, 0) <= rp.reorder_point`
	var args []any
	if warehouseID > 0 {
		query += " AND rp.warehouse_id = ?"
		args = append(args, warehouseID)
	}
	query += " ORDER BY rp.warehouse_id, p.sku"
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.LowStockRow{}
	for rows.Next() {
		var r domain.LowStockRow
		if err := rows.Scan(&r.ProductID, &r.SKU, &r.WarehouseID, &r.ReorderPoint, &r.Available); err != nil {
			return nil, err
		}
		r.Shortfall = r.ReorderPoint - r.Available
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reconcile compares every stock row with the net quantity of its ledger
// movements, and on-hand per product and warehouse with the remaining
// quantity of its cost layers.
func (q *Queries) Reconcile(ctx context.Context) (domain.Reconciliation, error) {
	rec := domain.Reconciliation{LedgerMismatches: []domain.LedgerMismatch{}, CostMismatches: []domain.CostMismatch{}}
	rows, err := q.db.QueryContext(ctx, `
		SELECT si.id, si.product_id, si.location_id, si.lot_id, si.on_hand, COALESCE(m.net, 0)
		FROM stock_items si
		LEFT JOIN (
			SELECT product_id, location_id, lot_key, SUM(qty) AS net FROM (
				SELECT product_id, to_location_id AS location_id, IFNULL(lot_id, 0) AS lot_key, quantity AS qty
				FROM stock_movements WHERE to_location_id IS NOT NULL
				UNION ALL
				SELECT product_id, from_location_id, IFNULL(lot_id, 0), -quantity
				FROM stock_movements WHERE from_location_id IS NOT NULL
			) x GROUP BY product_id, location_id, lot_key
		) m ON m.product_id = si.product_id AND m.location_id = si.location_id AND m.lot_key = si.lot_key
		WHERE si.on_hand <> COALESCE(m.net, 0)
		ORDER BY si.id`)
	if err != nil {
		return rec, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			m   domain.LedgerMismatch
			lot sql.NullInt64
		)
		if err := rows.Scan(&m.StockItemID, &m.ProductID, &m.LocationID, &lot, &m.OnHand, &m.LedgerQty); err != nil {
			return rec, err
		}
		m.LotID = ptrInt(lot)
		rec.LedgerMismatches = append(rec.LedgerMismatches, m)
	}
	if err := rows.Err(); err != nil {
		return rec, err
	}
	rows.Close()

	costRows, err := q.db.QueryContext(ctx, `
		SELECT k.product_id, k.warehouse_id, COALESCE(s.qty, 0), COALESCE(c.qty, 0)
		FROM (
			SELECT product_id, warehouse_id FROM stock_items
			UNION SELECT product_id, warehouse_id FROM cost_layers
		) k
		LEFT JOIN (SELECT product_id, warehouse_id, SUM(on_hand) AS qty FROM stock_items GROUP BY product_id, warehouse_id) s
			ON s.product_id = k.product_id AND s.warehouse_id = k.warehouse_id
		LEFT JOIN (SELECT product_id, warehouse_id, SUM(qty_remaining) AS qty FROM cost_layers GROUP BY product_id, warehouse_id) c
			ON c.product_id = k.product_id AND c.warehouse_id = k.warehouse_id
		WHERE COALESCE(s.qty, 0) <> COALESCE(c.qty, 0)
		ORDER BY k.product_id, k.warehouse_id`)
	if err != nil {
		return rec, err
	}
	defer costRows.Close()
	for costRows.Next() {
		var m domain.CostMismatch
		if err := costRows.Scan(&m.ProductID, &m.WarehouseID, &m.OnHand, &m.LayerQty); err != nil {
			return rec, err
		}
		rec.CostMismatches = append(rec.CostMismatches, m)
	}
	return rec, costRows.Err()
}
