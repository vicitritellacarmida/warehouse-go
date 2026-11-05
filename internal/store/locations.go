package store

import (
	"context"
	"database/sql"
	"slices"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
)

const locationCols = `id, warehouse_id, code, location_type, capacity, open_count_id, created_at`

func scanLocation(sc interface{ Scan(...any) error }) (*domain.Location, error) {
	var (
		l             domain.Location
		capacity, cnt sql.NullInt64
	)
	if err := sc.Scan(&l.ID, &l.WarehouseID, &l.Code, &l.Type, &capacity, &cnt, &l.CreatedAt); err != nil {
		return nil, err
	}
	l.Capacity, l.OpenCountID = ptrInt(capacity), ptrInt(cnt)
	return &l, nil
}

// CreateLocation inserts a location and sets l.ID.
func (q *Queries) CreateLocation(ctx context.Context, l *domain.Location) error {
	res, err := q.db.ExecContext(ctx,
		`INSERT INTO locations (warehouse_id, code, location_type, capacity, created_at) VALUES (?, ?, ?, ?, ?)`,
		l.WarehouseID, l.Code, l.Type, nullInt(l.Capacity), l.CreatedAt)
	if IsDuplicate(err) {
		return domain.Conflict(domain.CodeDuplicate, "location %s already exists in warehouse %d", l.Code, l.WarehouseID)
	}
	if err != nil {
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// GetLocation returns one location without locking it.
func (q *Queries) GetLocation(ctx context.Context, id int64) (*domain.Location, error) {
	l, err := scanLocation(q.db.QueryRowContext(ctx, `SELECT `+locationCols+` FROM locations WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, domain.NotFound("location", id)
	}
	return l, err
}

// ListLocations returns the locations of a warehouse ordered by code.
func (q *Queries) ListLocations(ctx context.Context, warehouseID int64) ([]*domain.Location, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT `+locationCols+` FROM locations WHERE warehouse_id = ? ORDER BY code`, warehouseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []*domain.Location{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

// LockLocations locks the given locations in id order and returns them.
// exclusive selects FOR UPDATE; otherwise FOR SHARE is used, which is
// enough to stop a cycle count from opening while stock moves.
func (q *Queries) LockLocations(ctx context.Context, ids []int64, exclusive map[int64]bool) (map[int64]*domain.Location, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make(map[int64]*domain.Location, len(ids))
	for _, id := range ids {
		mode := " FOR SHARE"
		if exclusive[id] {
			mode = " FOR UPDATE"
		}
		l, err := scanLocation(q.db.QueryRowContext(ctx, `SELECT `+locationCols+` FROM locations WHERE id = ?`+mode, id))
		if err == sql.ErrNoRows {
			return nil, domain.NotFound("location", id)
		}
		if err != nil {
			return nil, err
		}
		out[id] = l
	}
	return out, nil
}

// SetLocationCount sets or clears the open cycle count of a location.
func (q *Queries) SetLocationCount(ctx context.Context, locationID int64, countID *int64) error {
	_, err := q.db.ExecContext(ctx, `UPDATE locations SET open_count_id = ? WHERE id = ?`, nullInt(countID), locationID)
	return err
}

// LocationOnHand returns the total on-hand quantity in a location.
func (q *Queries) LocationOnHand(ctx context.Context, locationID int64) (int64, error) {
	var n int64
	err := q.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(on_hand), 0) FROM stock_items WHERE location_id = ?`, locationID).Scan(&n)
	return n, err
}
