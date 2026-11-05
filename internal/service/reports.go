package service

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// StockReport returns stock totals at the requested level of detail.
func (s *Service) StockReport(ctx context.Context, f store.StockFilter) ([]domain.StockRow, error) {
	if f.GroupBy == "" {
		f.GroupBy = domain.GroupByLocation
	}
	if !f.GroupBy.Valid() {
		return nil, domain.Invalid(domain.CodeValidation, "group_by must be one of product, warehouse, location, lot")
	}
	return s.store.Q().StockReport(ctx, f)
}

// ExpiringReport lists stock in lots expiring within days (including lots
// already expired).
func (s *Service) ExpiringReport(ctx context.Context, warehouseID int64, days int) ([]domain.ExpiringRow, error) {
	if days < 0 || days > 3650 {
		return nil, domain.Invalid(domain.CodeValidation, "days must be between 0 and 3650")
	}
	today := s.today()
	return s.store.Q().ExpiringStock(ctx, warehouseID, today.AddDays(days), today)
}

// LowStockReport lists products at or below their reorder point.
func (s *Service) LowStockReport(ctx context.Context, warehouseID int64) ([]domain.LowStockRow, error) {
	return s.store.Q().LowStock(ctx, warehouseID)
}

// ValuationReport is the FIFO inventory value per product and warehouse.
type ValuationReport struct {
	Rows       []domain.ValuationRow `json:"rows"`
	TotalValue int64                 `json:"total_value"`
	Currency   string                `json:"currency"`
}

// Valuation returns the FIFO inventory value from open cost layers. Values
// are in IDR minor units.
func (s *Service) Valuation(ctx context.Context, warehouseID int64) (*ValuationReport, error) {
	rows, err := s.store.Q().Valuation(ctx, warehouseID)
	if err != nil {
		return nil, err
	}
	rep := &ValuationReport{Rows: rows, Currency: "IDR"}
	for _, r := range rows {
		rep.TotalValue += r.Value
	}
	return rep, nil
}

// Reconcile checks stock rows against the ledger and cost layers.
func (s *Service) Reconcile(ctx context.Context) (domain.Reconciliation, error) {
	return s.store.Q().Reconcile(ctx)
}

// MovementPage is one page of movement history.
type MovementPage struct {
	Data       []domain.Movement `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ListMovements returns movement history newest first. The cursor is an
// opaque string from a previous page's NextCursor.
func (s *Service) ListMovements(ctx context.Context, f store.MovementFilter, cursor string) (*MovementPage, error) {
	if cursor != "" {
		id, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		f.BeforeID = id
	}
	limit := clampLimit(f.Limit)
	f.Limit = limit + 1
	list, err := s.store.Q().ListMovements(ctx, f)
	if err != nil {
		return nil, err
	}
	page := &MovementPage{Data: list}
	if len(list) > limit {
		page.Data = list[:limit]
		page.NextCursor = encodeCursor(page.Data[limit-1].ID)
	}
	return page, nil
}

func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte("mv:" + strconv.FormatInt(id, 10)))
}

func decodeCursor(c string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err == nil {
		if rest, ok := strings.CutPrefix(string(raw), "mv:"); ok {
			if id, err := strconv.ParseInt(rest, 10, 64); err == nil && id > 0 {
				return id, nil
			}
		}
	}
	return 0, domain.Invalid(domain.CodeValidation, "invalid cursor")
}
