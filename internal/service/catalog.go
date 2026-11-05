package service

import (
	"context"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// CreateProductInput is the data needed to create a product.
type CreateProductInput struct {
	SKU          string              `json:"sku"`
	Name         string              `json:"name"`
	BaseUOM      string              `json:"base_uom"`
	TrackingMode domain.TrackingMode `json:"tracking_mode"`
	Units        []domain.Unit       `json:"units"`
}

// CreateProduct validates and stores a new product.
func (s *Service) CreateProduct(ctx context.Context, in CreateProductInput) (*domain.Product, error) {
	p := &domain.Product{
		SKU: in.SKU, Name: in.Name, BaseUOM: in.BaseUOM, TrackingMode: in.TrackingMode,
		Units: in.Units, CreatedAt: s.now(),
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error { return q.CreateProduct(ctx, p) })
	if err != nil {
		return nil, err
	}
	return p, nil
}

// GetProduct returns a product by id.
func (s *Service) GetProduct(ctx context.Context, id int64) (*domain.Product, error) {
	return s.store.Q().GetProduct(ctx, id)
}

// ListProducts returns a page of products ordered by id.
func (s *Service) ListProducts(ctx context.Context, afterID int64, limit int) ([]*domain.Product, error) {
	return s.store.Q().ListProducts(ctx, afterID, clampLimit(limit))
}

// AddUnit adds a unit of measure to an existing product.
func (s *Service) AddUnit(ctx context.Context, productID int64, u domain.Unit) (*domain.Product, error) {
	u.UOM = domain.NormalizeUOM(u.UOM)
	if err := domain.ValidateUnit(u); err != nil {
		return nil, err
	}
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		if _, err := q.GetProduct(ctx, productID); err != nil {
			return err
		}
		return q.AddUnit(ctx, productID, u)
	})
	if err != nil {
		return nil, err
	}
	return s.GetProduct(ctx, productID)
}

// CreateWarehouseInput is the data needed to create a warehouse.
type CreateWarehouseInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// CreateWarehouse stores a new warehouse.
func (s *Service) CreateWarehouse(ctx context.Context, in CreateWarehouseInput) (*domain.Warehouse, error) {
	w := &domain.Warehouse{Code: in.Code, Name: in.Name, CreatedAt: s.now()}
	if err := w.Validate(); err != nil {
		return nil, err
	}
	if err := s.store.Q().CreateWarehouse(ctx, w); err != nil {
		return nil, err
	}
	return w, nil
}

// GetWarehouse returns a warehouse by id.
func (s *Service) GetWarehouse(ctx context.Context, id int64) (*domain.Warehouse, error) {
	return s.store.Q().GetWarehouse(ctx, id)
}

// ListWarehouses returns all warehouses.
func (s *Service) ListWarehouses(ctx context.Context) ([]domain.Warehouse, error) {
	return s.store.Q().ListWarehouses(ctx)
}

// CreateLocationInput is the data needed to create a location.
type CreateLocationInput struct {
	Code     string              `json:"code"`
	Type     domain.LocationType `json:"type"`
	Capacity *int64              `json:"capacity,omitempty"`
}

// CreateLocation adds a location to a warehouse.
func (s *Service) CreateLocation(ctx context.Context, warehouseID int64, in CreateLocationInput) (*domain.Location, error) {
	l := &domain.Location{WarehouseID: warehouseID, Code: in.Code, Type: in.Type, Capacity: in.Capacity, CreatedAt: s.now()}
	if err := l.Validate(); err != nil {
		return nil, err
	}
	q := s.store.Q()
	if _, err := q.GetWarehouse(ctx, warehouseID); err != nil {
		return nil, err
	}
	if err := q.CreateLocation(ctx, l); err != nil {
		return nil, err
	}
	return l, nil
}

// ListLocations returns the locations of a warehouse.
func (s *Service) ListLocations(ctx context.Context, warehouseID int64) ([]*domain.Location, error) {
	q := s.store.Q()
	if _, err := q.GetWarehouse(ctx, warehouseID); err != nil {
		return nil, err
	}
	return q.ListLocations(ctx, warehouseID)
}

// SetReorderPoint sets the reorder point of a product in a warehouse.
func (s *Service) SetReorderPoint(ctx context.Context, warehouseID, productID, point int64) error {
	if point < 0 {
		return domain.Invalid(domain.CodeValidation, "reorder_point must not be negative")
	}
	q := s.store.Q()
	if _, err := q.GetWarehouse(ctx, warehouseID); err != nil {
		return err
	}
	if _, err := q.GetProduct(ctx, productID); err != nil {
		return err
	}
	return q.SetReorderPoint(ctx, productID, warehouseID, point)
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 500:
		return 500
	}
	return n
}
