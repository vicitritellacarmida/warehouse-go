package httpapi

import (
	"net/http"

	"github.com/vicitritellacarmida/warehouse-go/internal/domain"
	"github.com/vicitritellacarmida/warehouse-go/internal/service"
	"github.com/vicitritellacarmida/warehouse-go/internal/store"
)

// withBody decodes the JSON body into In, runs fn and writes its result
// with the given status.
func withBody[In, Out any](a *API, w http.ResponseWriter, r *http.Request, status int, fn func(In) (Out, error)) {
	var in In
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := fn(in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, status, out)
}

// withID parses the {id} path value, runs fn and writes its result.
func withID[Out any](a *API, w http.ResponseWriter, r *http.Request, fn func(id int64) (Out, error)) {
	id, err := pathID(r, "id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out, err := fn(id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// withIDBody parses {id} and the JSON body, runs fn and writes its result.
// An empty body is accepted as the zero value of In.
func withIDBody[In, Out any](a *API, w http.ResponseWriter, r *http.Request, fn func(id int64, in In) (Out, error)) {
	id, err := pathID(r, "id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in In
	if err := decode(r, &in); err != nil && err != errEmptyBody {
		a.fail(w, r, err)
		return
	}
	out, err := fn(id, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Catalog

func (a *API) createProduct(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.CreateProductInput) (*domain.Product, error) {
		return a.svc.CreateProduct(r.Context(), in)
	})
}

func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	after, err := queryInt(r, "after_id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	limit, err := queryInt(r, "limit")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	list, err := a.svc.ListProducts(r.Context(), after, int(limit))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeList(w, list)
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Product, error) { return a.svc.GetProduct(r.Context(), id) })
}

func (a *API) addUnit(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, u domain.Unit) (*domain.Product, error) {
		return a.svc.AddUnit(r.Context(), id, u)
	})
}

func (a *API) createWarehouse(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.CreateWarehouseInput) (*domain.Warehouse, error) {
		return a.svc.CreateWarehouse(r.Context(), in)
	})
}

func (a *API) listWarehouses(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.ListWarehouses(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeList(w, list)
}

func (a *API) getWarehouse(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Warehouse, error) { return a.svc.GetWarehouse(r.Context(), id) })
}

func (a *API) createLocation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	withBody(a, w, r, http.StatusCreated, func(in service.CreateLocationInput) (*domain.Location, error) {
		return a.svc.CreateLocation(r.Context(), id, in)
	})
}

func (a *API) listLocations(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	list, err := a.svc.ListLocations(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeList(w, list)
}

type reorderPointBody struct {
	ReorderPoint int64 `json:"reorder_point"`
}

func (a *API) setReorderPoint(w http.ResponseWriter, r *http.Request) {
	productID, err := pathID(r, "productID")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	withIDBody(a, w, r, func(id int64, in reorderPointBody) (map[string]int64, error) {
		if err := a.svc.SetReorderPoint(r.Context(), id, productID, in.ReorderPoint); err != nil {
			return nil, err
		}
		return map[string]int64{"warehouse_id": id, "product_id": productID, "reorder_point": in.ReorderPoint}, nil
	})
}

// Inbound

func (a *API) createReceipt(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.CreateReceiptInput) (*domain.Receipt, error) {
		return a.svc.CreateReceipt(r.Context(), in)
	})
}

func (a *API) getReceipt(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Receipt, error) { return a.svc.GetReceipt(r.Context(), id) })
}

func (a *API) receive(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, in service.ReceiveInput) (*domain.Receipt, error) {
		return a.svc.Receive(r.Context(), id, in)
	})
}

func (a *API) closeReceipt(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, in service.CloseReceiptInput) (*domain.Receipt, error) {
		return a.svc.CloseReceipt(r.Context(), id, in)
	})
}

func (a *API) cancelReceipt(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Receipt, error) { return a.svc.CancelReceipt(r.Context(), id) })
}

func (a *API) putaway(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.MoveInput) (*domain.Movement, error) {
		return a.svc.Putaway(r.Context(), in)
	})
}

func (a *API) moveStock(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.MoveInput) (*domain.Movement, error) {
		return a.svc.MoveStock(r.Context(), in)
	})
}

// Outbound

func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.CreateOrderInput) (*service.OrderView, error) {
		return a.svc.CreateOrder(r.Context(), in)
	})
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*service.OrderView, error) { return a.svc.GetOrder(r.Context(), id) })
}

func (a *API) pickList(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (listBody[domain.PickTask], error) {
		tasks, err := a.svc.PickList(r.Context(), id)
		return listBody[domain.PickTask]{Data: tasks}, err
	})
}

func (a *API) allocateOrder(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*service.OrderView, error) { return a.svc.AllocateOrder(r.Context(), id) })
}

func (a *API) pickOrder(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, in service.PickInput) (*service.OrderView, error) {
		return a.svc.PickOrder(r.Context(), id, in)
	})
}

func (a *API) packOrder(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*service.OrderView, error) { return a.svc.PackOrder(r.Context(), id) })
}

func (a *API) shipOrder(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*service.OrderView, error) { return a.svc.ShipOrder(r.Context(), id) })
}

func (a *API) cancelOrder(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*service.OrderView, error) { return a.svc.CancelOrder(r.Context(), id) })
}

// Transfers

func (a *API) createTransfer(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.CreateTransferInput) (*domain.Transfer, error) {
		return a.svc.CreateTransfer(r.Context(), in)
	})
}

func (a *API) getTransfer(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Transfer, error) { return a.svc.GetTransfer(r.Context(), id) })
}

func (a *API) shipTransfer(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Transfer, error) { return a.svc.ShipTransfer(r.Context(), id) })
}

func (a *API) receiveTransfer(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, in service.ReceiveTransferInput) (*domain.Transfer, error) {
		return a.svc.ReceiveTransfer(r.Context(), id, in)
	})
}

func (a *API) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.Transfer, error) { return a.svc.CancelTransfer(r.Context(), id) })
}

// Cycle counts

func (a *API) openCount(w http.ResponseWriter, r *http.Request) {
	withBody(a, w, r, http.StatusCreated, func(in service.OpenCountInput) (*domain.CycleCount, error) {
		return a.svc.OpenCount(r.Context(), in)
	})
}

func (a *API) getCount(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.CycleCount, error) { return a.svc.GetCount(r.Context(), id) })
}

func (a *API) recordCount(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, in service.RecordCountInput) (*domain.CycleCount, error) {
		return a.svc.RecordCount(r.Context(), id, in)
	})
}

func (a *API) postCount(w http.ResponseWriter, r *http.Request) {
	withIDBody(a, w, r, func(id int64, in service.PostCountInput) (*domain.CycleCount, error) {
		return a.svc.PostCount(r.Context(), id, in)
	})
}

func (a *API) cancelCount(w http.ResponseWriter, r *http.Request) {
	withID(a, w, r, func(id int64) (*domain.CycleCount, error) { return a.svc.CancelCount(r.Context(), id) })
}

// Reports

func (a *API) stockReport(w http.ResponseWriter, r *http.Request) {
	var f store.StockFilter
	var err error
	if f.ProductID, err = queryInt(r, "product_id"); err != nil {
		a.fail(w, r, err)
		return
	}
	if f.WarehouseID, err = queryInt(r, "warehouse_id"); err != nil {
		a.fail(w, r, err)
		return
	}
	if f.LocationID, err = queryInt(r, "location_id"); err != nil {
		a.fail(w, r, err)
		return
	}
	f.GroupBy = domain.StockGrouping(r.URL.Query().Get("group_by"))
	rows, err := a.svc.StockReport(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeList(w, rows)
}

func (a *API) listMovements(w http.ResponseWriter, r *http.Request) {
	var (
		f   store.MovementFilter
		err error
		lim int64
	)
	for name, dst := range map[string]*int64{
		"product_id": &f.ProductID, "warehouse_id": &f.WarehouseID, "location_id": &f.LocationID,
		"reference_id": &f.ReferenceID, "limit": &lim,
	} {
		if *dst, err = queryInt(r, name); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	f.Limit = int(lim)
	f.Type = domain.MovementType(r.URL.Query().Get("movement_type"))
	f.ReferenceType = r.URL.Query().Get("reference_type")
	page, err := a.svc.ListMovements(r.Context(), f, r.URL.Query().Get("cursor"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *API) expiringReport(w http.ResponseWriter, r *http.Request) {
	wh, err := queryInt(r, "warehouse_id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	days := int64(30)
	if r.URL.Query().Has("days") {
		if days, err = queryInt(r, "days"); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	rows, err := a.svc.ExpiringReport(r.Context(), wh, int(days))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeList(w, rows)
}

func (a *API) lowStockReport(w http.ResponseWriter, r *http.Request) {
	wh, err := queryInt(r, "warehouse_id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rows, err := a.svc.LowStockReport(r.Context(), wh)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeList(w, rows)
}

func (a *API) valuationReport(w http.ResponseWriter, r *http.Request) {
	wh, err := queryInt(r, "warehouse_id")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rep, err := a.svc.Valuation(r.Context(), wh)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (a *API) reconciliation(w http.ResponseWriter, r *http.Request) {
	rec, err := a.svc.Reconcile(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"consistent":        rec.Consistent(),
		"ledger_mismatches": rec.LedgerMismatches,
		"cost_mismatches":   rec.CostMismatches,
	})
}
