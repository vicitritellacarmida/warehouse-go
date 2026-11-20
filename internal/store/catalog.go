package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

// CreateProduct inserts a product and its units and sets p.ID.
func (q *Queries) CreateProduct(ctx context.Context, p *domain.Product) error {
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO products (sku, name, base_uom, tracking_mode, created_at) VALUES (?, ?, ?, ?, ?)`,
		p.SKU, p.Name, p.BaseUOM, p.TrackingMode, p.CreatedAt)
	if IsDuplicate(err) {
		return domain.Conflict(domain.CodeDuplicate, "a product with sku %s already exists", p.SKU)
	}
	if err != nil {
		return err
	}
	if p.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	for _, u := range p.Units {
		if err := q.AddUnit(ctx, p.ID, u); err != nil {
			return err
		}
	}
	return nil
}

// AddUnit adds a unit of measure to a product.
func (q *Queries) AddUnit(ctx context.Context, productID int64, u domain.Unit) error {
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO product_units (product_id, uom, factor) VALUES (?, ?, ?)`, productID, u.UOM, u.Factor)
	if IsDuplicate(err) {
		return domain.Conflict(domain.CodeDuplicate, "unit %s already exists for product %d", u.UOM, productID)
	}
	return err
}

const productCols = `id, sku, name, base_uom, tracking_mode, created_at`

func scanProduct(sc interface{ Scan(...any) error }) (*domain.Product, error) {
	var p domain.Product
	if err := sc.Scan(&p.ID, &p.SKU, &p.Name, &p.BaseUOM, &p.TrackingMode, &p.CreatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetProduct returns a product with its units.
func (q *Queries) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	m, err := q.GetProducts(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	p, ok := m[id]
	if !ok {
		return nil, domain.NotFound("product", id)
	}
	return p, nil
}

// GetProducts returns the products with the given ids, keyed by id. Missing
// ids are reported as product_not_found.
func (q *Queries) GetProducts(ctx context.Context, ids []int64) (map[int64]*domain.Product, error) {
	out := map[int64]*domain.Product{}
	if len(ids) == 0 {
		return out, nil
	}
	in, args := inClause(ids)
	rows, err := q.db.QueryContext(ctx, `SELECT `+productCols+` FROM products WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, domain.NotFound("product", id)
		}
	}
	return out, q.loadUnits(ctx, out)
}

func (q *Queries) loadUnits(ctx context.Context, products map[int64]*domain.Product) error {
	ids := make([]int64, 0, len(products))
	for id := range products {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	in, args := inClause(ids)
	rows, err := q.db.QueryContext(ctx,
		`SELECT product_id, uom, factor FROM product_units WHERE product_id IN (`+in+`) ORDER BY factor, uom`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid int64
		var u domain.Unit
		if err := rows.Scan(&pid, &u.UOM, &u.Factor); err != nil {
			return err
		}
		products[pid].Units = append(products[pid].Units, u)
	}
	return rows.Err()
}

// ListProducts returns up to limit products with id greater than afterID.
func (q *Queries) ListProducts(ctx context.Context, afterID int64, limit int) ([]*domain.Product, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT `+productCols+` FROM products WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*domain.Product
	byID := map[int64]*domain.Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, p)
		byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, q.loadUnits(ctx, byID)
}

// CreateWarehouse inserts a warehouse and sets w.ID.
func (q *Queries) CreateWarehouse(ctx context.Context, w *domain.Warehouse) error {
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO warehouses (code, name, created_at) VALUES (?, ?, ?)`, w.Code, w.Name, w.CreatedAt)
	if IsDuplicate(err) {
		return domain.Conflict(domain.CodeDuplicate, "a warehouse with code %s already exists", w.Code)
	}
	if err != nil {
		return err
	}
	w.ID, err = res.LastInsertId()
	return err
}

// GetWarehouse returns one warehouse.
func (q *Queries) GetWarehouse(ctx context.Context, id int64) (*domain.Warehouse, error) {
	var w domain.Warehouse
	err := q.db.QueryRowContext(ctx, `SELECT id, code, name, created_at FROM warehouses WHERE id = ?`, id).
		Scan(&w.ID, &w.Code, &w.Name, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NotFound("warehouse", id)
	}
	return &w, err
}

// ListWarehouses returns all warehouses ordered by id.
func (q *Queries) ListWarehouses(ctx context.Context) ([]domain.Warehouse, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id, code, name, created_at FROM warehouses ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []domain.Warehouse{}
	for rows.Next() {
		var w domain.Warehouse
		if err := rows.Scan(&w.ID, &w.Code, &w.Name, &w.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, w)
	}
	return list, rows.Err()
}

// SetReorderPoint creates or replaces the reorder point of a product in a
// warehouse.
func (q *Queries) SetReorderPoint(ctx context.Context, productID, warehouseID, point int64) error {
	_, err := q.db.ExecContext(ctx, `
		INSERT INTO reorder_points (product_id, warehouse_id, reorder_point, updated_at)
		VALUES (?, ?, ?, UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE reorder_point = ?, updated_at = UTC_TIMESTAMP(6)`,
		productID, warehouseID, point, point)
	return err
}
