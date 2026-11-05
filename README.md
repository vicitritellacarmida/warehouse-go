# warehouse-go

warehouse-go is an inventory and warehouse management service for a fictional retail and distribution business. It is written in Go and stores data in MySQL 8. It exposes a JSON HTTP API.

The service does these things:

- It keeps a product catalog with units of measure and lot tracking.
- It models warehouses and the bins (locations) inside them.
- It receives goods from suppliers and puts them away.
- It reserves, picks, packs and ships customer orders.
- It moves stock between warehouses.
- It counts bins and corrects the stock.
- It calculates the cost of stock with FIFO cost layers.
- It reports stock, expiry, low stock, value and movement history.

Every change to stock goes through an append-only movement ledger. All quantities and amounts of money are integers.

## Warehouse concepts for backend engineers

This section explains the logistics terms that the code uses. You do not need warehouse experience to read it.

### Products, units and base quantities

A product is one SKU (stock keeping unit), for example "Jasmine tea 330ml". Each product has a base unit of measure, for example `PCS` (pieces). The service stores every quantity in the base unit as a `BIGINT`.

A product can have more units, for example `BOX` with factor 24. A request can say "2 BOX". The service converts it to 48 PCS before it stores anything. This keeps arithmetic exact. There are no fractions of a piece.

### Warehouses and locations

A warehouse is a building. A location is one bin or area inside it. Each location has a type:

| Type | Purpose |
| --- | --- |
| `receiving` | Goods from suppliers and transfers arrive here first. |
| `storage` | Long-term shelves. Orders take stock from here. |
| `picking` | Shelves near packing. Orders also take stock from here. |
| `shipping` | Picked goods wait here before they leave. |
| `quarantine` | Damaged or suspect goods. This stock is never available. |

A location can have a capacity. The capacity is the maximum on-hand quantity in base units. The service rejects a movement that puts more into the location.

### On hand, reserved and available

These three numbers are the core of the stock model:

- On hand is the quantity that is physically in a location.
- Reserved is the part of on hand that is promised to an order.
- Available is on hand minus reserved. It is what a new order can still get.

Stock in a quarantine location has an available quantity of 0, always. Stock in a receiving location counts as available in reports, but orders do not take it. Orders take stock only from storage and picking locations, so goods must be put away first.

The service stores one row per product, location and lot in the `stock_items` table. A database `CHECK` makes sure that `0 <= reserved <= on_hand`. Every update to the row is guarded in SQL as well.

### Lots and expiry dates

A lot (also called a batch) is a group of units that were made or delivered together. Each product has a tracking mode:

- `none`: the service does not track lots.
- `lot`: each unit belongs to a lot number. Lots have no expiry date.
- `lot_expiry`: each lot has a lot number and an expiry date.

Lot numbers are unique per product. If the same lot arrives again with a different expiry date, the service rejects it.

### FEFO and FIFO

When an order needs stock, the service must decide which units to take.

FEFO means "first expiry, first out". The service takes the lot with the earliest expiry date first. This keeps old stock from expiring on the shelf. The service uses FEFO for `lot_expiry` products. It skips lots with an expiry date before today. A lot that expires today is still usable.

FIFO means "first in, first out". The service takes the units that arrived first. It uses FIFO for products without expiry dates. Each stock row stores `received_at`, the receipt time of the oldest units in that row. Moves keep this time.

Ties are broken by receipt time and then by stock row id, so the result is deterministic. The pure algorithm is in `internal/domain/allocation.go`.

### Reservation, picking, packing and shipping

An order goes through these steps:

1. Allocate: the service reserves stock with FEFO or FIFO. Reserved increases. On hand stays the same.
2. Pick: a worker moves the reserved units to a shipping location. The units stay reserved.
3. Pack: the worker packs the box. This is a status change only.
4. Ship: the units leave the shipping location. On hand and reserved decrease.

If the service cannot cover the full quantity, it allocates what it can. It moves the rest to a new backorder. A backorder is a new order with status `new` and the order number plus `-BO`. You allocate the backorder later, when stock arrives. If nothing can be allocated, the call fails with `insufficient_stock` and the order stays `new`.

Cancel releases all reservations. Units that were already picked stay in the shipping location as free stock. You can move them back with `POST /api/v1/stock/moves`.

### Status rules

Order statuses:

| From | Allowed next status |
| --- | --- |
| `new` | `allocated`, `cancelled` |
| `allocated` | `picked`, `cancelled` |
| `picked` | `packed`, `cancelled` |
| `packed` | `shipped`, `cancelled` |
| `shipped`, `cancelled` | none |

Order line statuses: `open` to `allocated` or `backordered`. Then `allocated` to `picked` to `shipped`. A line that is not shipped can go to `cancelled`.

Receipts: `open` to `receiving` to `closed`. An `open` receipt can also go to `cancelled`.

Transfers: `draft` to `in_transit` to `received` or `received_with_discrepancy`. A `draft` transfer can also go to `cancelled`.

Cycle counts: `open` to `posted` or `cancelled`.

The rules are in `internal/domain/state.go`. A forbidden change returns HTTP 409 with code `invalid_transition`.

### Receipts and tolerance

A receipt lists the expected quantity of each product from a supplier. Workers report what actually arrived. A tolerance in basis points (100 bp = 1%) controls how much the received quantity can differ from the expected quantity:

- Over receipt: the service rejects a receive that puts the line above expected plus tolerance.
- Under receipt: a line below expected minus tolerance is `short`. To close a receipt with short lines, send `"accept_short": true`.

The tolerance is rounded down. For example, 5% of 10 units is 0 units, so a line of 10 units accepts exactly 10.

### Put-away

Put-away moves goods from a receiving location to a storage, picking or quarantine location. It is a normal ledger movement of type `putaway`.

### Transfers and in-transit stock

A transfer moves stock from one warehouse to another. When the transfer ships, the units leave the source warehouse. They are then in transit. In-transit stock is in no location and in no warehouse on-hand quantity. The transfer record keeps the shipped lots and their costs.

When the destination receives the transfer, it books the units into a receiving location with the same lots and the same FIFO receipt times. If fewer units arrive than were shipped, the difference is a discrepancy. The transfer ends in `received_with_discrepancy`. Each line shows the missing quantity and its value at FIFO cost. The service rejects a receive of more units than were shipped.

### Cycle counts

A cycle count is a physical count of one location. You count a few locations every day instead of the whole building once a year.

1. Open the count. The service saves the system quantity of each product and lot in the location. It also blocks the location.
2. Record the counted quantities. You can also report a lot that the system did not know about.
3. Post the count with a reason (`damaged`, `lost`, `found`, `miscount`, `expired` or `other`). The service writes an `adjustment` movement for each difference and unblocks the location.

While a count is open, the service rejects every movement into or out of the location with `location_count_open`. Orders also do not allocate from it.

### Cost layers and valuation

The service values stock with FIFO costing. A cost layer is a quantity of one product that entered one warehouse at one unit cost. The unit cost is in IDR minor units per base unit, as a `BIGINT`.

- A receipt creates a layer at the unit cost of the receipt line.
- A shipment consumes layers, oldest first. The consumed cost is the cost of goods sold (COGS). The service stores it on each shipment line.
- A transfer consumes layers at the source and creates layers at the destination with the same unit costs.
- A count gain creates a layer at the latest known unit cost of the product in that warehouse, or 0. A count loss consumes layers.

Cost layers belong to a product and a warehouse, not to a lot or a location. Physical picking can use FEFO while costing uses FIFO by receipt time. This is normal: the two answer different questions.

The valuation report sums the remaining quantity times the unit cost of all open layers.

### Ledger and reconciliation

`stock_movements` is append-only. MySQL triggers reject `UPDATE` and `DELETE` on the table. Each movement has a type, a product, an optional lot, an optional source location, an optional destination location, a positive quantity and a reference document.

The service updates `stock_items` and inserts the movement in the same transaction. `GET /api/v1/reports/reconciliation` recalculates on hand from the ledger and compares it with `stock_items`. It also compares on hand with the remaining quantity of the cost layers. The integration tests call it after every flow.

### Concurrency

Two orders must never reserve the same units. The service uses these rules:

- Each write runs in one `READ COMMITTED` transaction.
- Allocation locks all candidate stock rows with one `SELECT ... FOR UPDATE` statement ordered by `id`. A second allocation waits for the first one and then reads the new reserved quantity.
- All writes lock rows in the same order: the document row first, then locations by id, then stock rows by id, then cost layers oldest first.
- If MySQL reports a deadlock (1213) or a lock wait timeout (1205), the store retries the whole transaction up to 5 times.
- `UPDATE` statements on stock rows have guards (`on_hand + delta >= reserved + delta`). If a lock is missing, these guards still stop an oversell.

`TestConcurrentAllocationNeverOversells` starts 30 allocations of 7 units at the same time against 100 units. It makes sure that exactly 100 units are reserved.

### Idempotency

A client can send an `Idempotency-Key` header on any `POST` or `PUT` request. The key is scoped to the method and the path.

- First request with a key: the service runs it and stores the status and the body.
- Same key and the same body: the service returns the stored response with the header `Idempotent-Replayed: true`. It does not run the request again.
- Same key and a different body: the service returns HTTP 422 with code `idempotency_key_reused`.
- Same key, and the first request is still running: HTTP 409 with code `idempotency_in_progress`.

The service does not store responses with status 500 or higher, so the client can retry them. Requests without the header work as usual.

## Requirements

- Go 1.22 or later.
- MySQL 8.0.19 or later. The SQL uses `INSERT ... AS new ON DUPLICATE KEY UPDATE`, `FOR UPDATE OF`, `CHECK` constraints and generated columns.
- The MySQL user must be able to create triggers. If binary logging is on, the user needs the `TRIGGER` privilege and `log_bin_trust_function_creators=1`, or the `SUPER` privilege.

## Setup

1. Create a database and a user:

   ```sql
   CREATE DATABASE warehouse CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
   CREATE USER 'warehouse'@'%' IDENTIFIED BY 'YOUR_PASSWORD_HERE';
   GRANT ALL PRIVILEGES ON warehouse.* TO 'warehouse'@'%';
   ```

2. Copy the example configuration:

   ```sh
   cp .env.example .env
   ```

3. Set `DATABASE_DSN` in `.env`. Replace `YOUR_PASSWORD_HERE` with the real password. If the DSN contains the placeholder, the service does not start.

If a `.env` file exists in the current directory, the service reads it. Variables in the environment win over values in `.env`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_DSN` | `root:YOUR_PASSWORD_HERE@tcp(127.0.0.1:3306)/warehouse?parseTime=true&multiStatements=true` | MySQL DSN in go-sql-driver format. The service always sets `parseTime=true` and UTC. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Logs are JSON on stdout. |
| `AUTO_MIGRATE` | `true` | If `true`, the server applies pending migrations at startup. |
| `RECEIPT_TOLERANCE_BP` | `500` | Default receipt tolerance in basis points. |
| `SHUTDOWN_TIMEOUT` | `15s` | Time to finish open requests on shutdown. |

## Running

```sh
make migrate   # apply migrations and exit
make run       # start the server (it also migrates, see AUTO_MIGRATE)
make build     # build bin/warehouse
```

The migration runner is in `internal/migrate`. It embeds `migrations/*.sql` into the binary, takes a MySQL advisory lock, and applies files in version order. It records each applied file in `schema_migrations`. MySQL commits DDL statements at once. If a file fails in the middle, part of it can stay applied, so fix the database by hand before you run the migration again.

## Tests

```sh
make test               # unit tests, no database needed
make test-integration   # unit and integration tests against MySQL, with -race
make lint               # gofmt -l and go vet
```

`go test ./...` passes without a database. If `TEST_DATABASE_DSN` is not set, the integration tests skip with a message.

To run the integration tests, set `TEST_DATABASE_DSN` to a DSN whose database name ends with `_test`. The tests refuse any other name. Each test package creates its own database from that name. For example, `warehouse_test` becomes `warehouse_service_test` and `warehouse_httpapi_test`. Each package drops and creates its database, applies the migrations, and drops the database at the end.

```sh
TEST_DATABASE_DSN='root:YOUR_PASSWORD_HERE@tcp(127.0.0.1:3306)/warehouse_test' make test-integration
```

The tests cover these areas:

- `internal/domain`: table-driven tests for FEFO and FIFO allocation, FIFO costing, unit conversion, state machines, receipt tolerance and transfer discrepancies.
- `internal/store`: migrations run twice, the append-only trigger, stock row guards and transaction rollback.
- `internal/service`: full flows for receipts, orders, backorders, cancel, quarantine, capacity, transfers, cycle counts and reports, plus two concurrency tests.
- `internal/httpapi`: an end-to-end flow over HTTP, idempotency and error bodies.

## API

All paths start with `/api/v1` except `/healthz`. Request and response bodies are JSON. List responses have the form `{"data": [...]}`. Every response has an `X-Request-ID` header.

Quantities in requests are in the unit given by `uom`. If `uom` is empty, the base unit is used. Quantities in responses are always in base units. Unit costs are in IDR minor units per base unit.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Health check with a database ping. |
| POST | `/api/v1/products` | Create a product with units. |
| GET | `/api/v1/products?after_id=&limit=` | List products by id. |
| GET | `/api/v1/products/{id}` | Get a product. |
| POST | `/api/v1/products/{id}/units` | Add a unit, body `{"uom":"PALLET","factor":960}`. |
| POST | `/api/v1/warehouses` | Create a warehouse. |
| GET | `/api/v1/warehouses` | List warehouses. |
| GET | `/api/v1/warehouses/{id}` | Get a warehouse. |
| POST | `/api/v1/warehouses/{id}/locations` | Create a location. |
| GET | `/api/v1/warehouses/{id}/locations` | List locations. |
| PUT | `/api/v1/warehouses/{id}/reorder-points/{productID}` | Set the reorder point, body `{"reorder_point":500}`. |
| POST | `/api/v1/receipts` | Create a receipt with expected lines. |
| GET | `/api/v1/receipts/{id}` | Get a receipt. |
| POST | `/api/v1/receipts/{id}/receive` | Book received goods into a receiving location. |
| POST | `/api/v1/receipts/{id}/close` | Close a receipt, optional `{"accept_short":true}`. |
| POST | `/api/v1/receipts/{id}/cancel` | Cancel a receipt with nothing received. |
| POST | `/api/v1/putaways` | Move stock from receiving to storage, picking or quarantine. |
| POST | `/api/v1/stock/moves` | Move free stock between locations of one warehouse. |
| POST | `/api/v1/orders` | Create a sales order. |
| GET | `/api/v1/orders/{id}` | Get an order with allocations and shipment. |
| GET | `/api/v1/orders/{id}/pick-list` | Get pick instructions by location and lot. |
| POST | `/api/v1/orders/{id}/allocate` | Reserve stock (FEFO or FIFO), maybe create a backorder. |
| POST | `/api/v1/orders/{id}/pick` | Move reserved stock to a shipping location. |
| POST | `/api/v1/orders/{id}/pack` | Mark the order as packed. |
| POST | `/api/v1/orders/{id}/ship` | Ship the order and record COGS. |
| POST | `/api/v1/orders/{id}/cancel` | Cancel the order and release reservations. |
| POST | `/api/v1/transfers` | Create a draft transfer. |
| GET | `/api/v1/transfers/{id}` | Get a transfer. |
| POST | `/api/v1/transfers/{id}/ship` | Ship the transfer (stock goes in transit). |
| POST | `/api/v1/transfers/{id}/receive` | Receive the transfer at the destination. |
| POST | `/api/v1/transfers/{id}/cancel` | Cancel a draft transfer. |
| POST | `/api/v1/cycle-counts` | Open a count on a location. |
| GET | `/api/v1/cycle-counts/{id}` | Get a count with variances. |
| POST | `/api/v1/cycle-counts/{id}/lines` | Record counted quantities. |
| POST | `/api/v1/cycle-counts/{id}/post` | Post adjustments with a reason. |
| POST | `/api/v1/cycle-counts/{id}/cancel` | Cancel a count. |
| GET | `/api/v1/stock?group_by=&product_id=&warehouse_id=&location_id=` | Stock report. `group_by` is `product`, `warehouse`, `location` (default) or `lot`. |
| GET | `/api/v1/movements?product_id=&warehouse_id=&location_id=&movement_type=&reference_type=&reference_id=&limit=&cursor=` | Movement history, newest first, with cursor pagination. |
| GET | `/api/v1/reports/expiring?days=30&warehouse_id=` | Stock in lots that expire within the given days, including expired lots. |
| GET | `/api/v1/reports/low-stock?warehouse_id=` | Products with available stock at or below the reorder point. |
| GET | `/api/v1/reports/valuation?warehouse_id=` | FIFO value per product and warehouse. |
| GET | `/api/v1/reports/reconciliation` | Compare stock rows with the ledger and the cost layers. |

### Examples

The responses below come from a real run. Timestamps are shortened.

Create a warehouse and a location:

```sh
curl -s -X POST localhost:8080/api/v1/warehouses \
  -H 'Content-Type: application/json' \
  -d '{"code":"JKT1","name":"Jakarta DC"}'
```

```json
{"id":1,"code":"JKT1","name":"Jakarta DC","created_at":"2026-11-05T06:12:43.744093Z"}
```

```sh
curl -s -X POST localhost:8080/api/v1/warehouses/1/locations \
  -H 'Content-Type: application/json' \
  -d '{"code":"A-01-01","type":"storage","capacity":5000}'
```

```json
{"id":2,"warehouse_id":1,"code":"A-01-01","type":"storage","capacity":5000,"created_at":"..."}
```

Create a product with a box of 24:

```sh
curl -s -X POST localhost:8080/api/v1/products \
  -H 'Content-Type: application/json' \
  -d '{"sku":"TEA-JAS-330","name":"Jasmine tea 330ml","base_uom":"PCS","tracking_mode":"lot_expiry","units":[{"uom":"BOX","factor":24}]}'
```

```json
{"id":1,"sku":"TEA-JAS-330","name":"Jasmine tea 330ml","base_uom":"PCS","tracking_mode":"lot_expiry",
 "units":[{"uom":"PCS","factor":1},{"uom":"BOX","factor":24}],"created_at":"..."}
```

Create a receipt for 10 boxes at Rp 4,500 per piece (450000 minor units), then receive it into location 1 (`RCV-01`):

```sh
curl -s -X POST localhost:8080/api/v1/receipts \
  -H 'Content-Type: application/json' \
  -d '{"warehouse_id":1,"reference":"PO-1001","supplier":"Sample Supplier","lines":[{"product_id":1,"quantity":10,"uom":"BOX","unit_cost":450000}]}'

curl -s -X POST localhost:8080/api/v1/receipts/1/receive \
  -H 'Content-Type: application/json' \
  -d '{"location_id":1,"lines":[{"line_id":1,"quantity":240,"lot_number":"LOT-A","expiry_date":"2027-03-31"}]}'
```

```json
{"id":1,"warehouse_id":1,"reference":"PO-1001","supplier":"Sample Supplier","status":"receiving","tolerance_bp":500,
 "lines":[{"id":1,"receipt_id":1,"product_id":1,"expected_qty":240,"received_qty":240,"unit_cost":450000,"outcome":"complete"}],
 "created_at":"..."}
```

Put the stock away:

```sh
curl -s -X POST localhost:8080/api/v1/putaways \
  -H 'Content-Type: application/json' \
  -d '{"product_id":1,"lot_id":1,"from_location_id":1,"to_location_id":2,"quantity":240}'
```

```json
{"id":2,"movement_type":"putaway","product_id":1,"warehouse_id":1,"lot_id":1,"from_location_id":1,"to_location_id":2,
 "quantity":240,"reference_type":"stock_move","reference_id":0,"created_at":"..."}
```

Create an order with an idempotency key, then allocate it:

```sh
curl -s -X POST localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: 3f1c2a9e' \
  -d '{"order_number":"SO-5001","warehouse_id":1,"customer":"Sample Customer","lines":[{"product_id":1,"quantity":2,"uom":"BOX"}]}'

curl -s -X POST localhost:8080/api/v1/orders/1/allocate
```

```json
{"id":1,"order_number":"SO-5001","warehouse_id":1,"customer":"Sample Customer","status":"allocated",
 "lines":[{"id":1,"order_id":1,"product_id":1,"quantity":48,"qty_allocated":48,"qty_backordered":0,"qty_picked":0,"qty_shipped":0,"status":"allocated"}],
 "created_at":"...","updated_at":"...",
 "allocations":[{"id":1,"order_id":1,"order_line_id":1,"product_id":1,"stock_item_id":2,"location_id":2,"lot_id":1,"quantity":48,"status":"reserved"}]}
```

Pick list, pick, pack and ship:

```sh
curl -s localhost:8080/api/v1/orders/1/pick-list
curl -s -X POST localhost:8080/api/v1/orders/1/pick -H 'Content-Type: application/json' -d '{"shipping_location_id":3}'
curl -s -X POST localhost:8080/api/v1/orders/1/pack
curl -s -X POST localhost:8080/api/v1/orders/1/ship
```

```json
{"data":[{"allocation_id":1,"order_line_id":1,"product_id":1,"sku":"TEA-JAS-330","location_id":2,"location_code":"A-01-01",
 "lot_id":1,"lot_number":"LOT-A","expiry_date":"2027-03-31","quantity":48,"status":"reserved"}]}
```

The ship response contains the shipment with COGS:

```json
"shipment":{"id":1,"order_id":1,"total_cogs":21600000,
 "lines":[{"id":1,"order_line_id":1,"product_id":1,"lot_id":1,"quantity":48,"cogs":21600000}],"shipped_at":"..."}
```

Stock by lot, movement history and valuation:

```sh
curl -s 'localhost:8080/api/v1/stock?group_by=lot'
curl -s 'localhost:8080/api/v1/movements?limit=2'
curl -s 'localhost:8080/api/v1/reports/valuation'
```

```json
{"data":[{"product_id":1,"sku":"TEA-JAS-330","warehouse_id":1,"lot_id":1,"lot_number":"LOT-A","expiry_date":"2027-03-31",
 "on_hand":192,"reserved":0,"available":192,"quarantined":0}]}
```

```json
{"data":[{"id":4,"movement_type":"ship","product_id":1,"warehouse_id":1,"lot_id":1,"from_location_id":3,"quantity":48,
 "reference_type":"shipment","reference_id":1,"created_at":"..."},
 {"id":3,"movement_type":"pick","product_id":1,"warehouse_id":1,"lot_id":1,"from_location_id":2,"to_location_id":3,
 "quantity":48,"reference_type":"sales_order","reference_id":1,"created_at":"..."}],
 "next_cursor":"bXY6Mw"}
```

```json
{"rows":[{"product_id":1,"sku":"TEA-JAS-330","warehouse_id":1,"quantity":192,"value":86400000}],"total_value":86400000,"currency":"IDR"}
```

To read the next page of movements, send the cursor: `GET /api/v1/movements?limit=2&cursor=bXY6Mw`. The last page has no `next_cursor`.

Low stock after a reorder point of 500:

```json
{"data":[{"product_id":1,"sku":"TEA-JAS-330","warehouse_id":1,"reorder_point":500,"available":192,"shortfall":308}]}
```

Transfer 2 boxes to a second warehouse, and receive 46 of the 48 units:

```sh
curl -s -X POST localhost:8080/api/v1/transfers -H 'Content-Type: application/json' \
  -d '{"from_warehouse_id":1,"to_warehouse_id":2,"lines":[{"product_id":1,"quantity":2,"uom":"BOX"}]}'
curl -s -X POST localhost:8080/api/v1/transfers/1/ship
curl -s -X POST localhost:8080/api/v1/transfers/1/receive -H 'Content-Type: application/json' \
  -d '{"location_id":4,"lines":[{"line_id":1,"quantity":46}]}'
```

```json
{"id":1,"from_warehouse_id":1,"to_warehouse_id":2,"status":"received_with_discrepancy",
 "lines":[{"id":1,"transfer_id":1,"product_id":1,"quantity":48,"qty_shipped":48,"qty_received":46,"discrepancy":2,"discrepancy_value":900000,
 "lots":[{"id":1,"lot_id":1,"received_at":"...","quantity":48,"qty_received":46}],
 "costs":[{"id":2,"unit_cost":450000,"received_at":"...","quantity":48,"qty_received":46}]}],
 "created_at":"...","shipped_at":"...","received_at":"..."}
```

If you send `{"location_id":4}` without lines, the service receives every line in full.

Cycle count of location 2:

```sh
curl -s -X POST localhost:8080/api/v1/cycle-counts -H 'Content-Type: application/json' -d '{"location_id":2}'
curl -s -X POST localhost:8080/api/v1/cycle-counts/1/lines -H 'Content-Type: application/json' \
  -d '{"lines":[{"product_id":1,"lot_number":"LOT-A","expiry_date":"2027-03-31","counted_qty":190}]}'
curl -s -X POST localhost:8080/api/v1/cycle-counts/1/post -H 'Content-Type: application/json' \
  -d '{"reason":"damaged","note":"two crushed cases"}'
```

```json
{"id":1,"location_id":2,"warehouse_id":1,"status":"posted","reason":"damaged","note":"two crushed cases",
 "lines":[{"id":1,"cycle_count_id":1,"product_id":1,"lot_id":1,"system_qty":192,"counted_qty":190,"variance":-2}],
 "created_at":"...","posted_at":"..."}
```

## Errors

Every error has the same body:

```json
{"error":{"code":"invalid_transition","message":"order cannot move from picked to shipped",
 "details":{"from":"picked","to":"shipped"},"request_id":"1edcde42-80d9-4224-9722-f98c698367d7"}}
```

Clients must use `code`. The `message` text can change.

| HTTP | Code | Meaning |
| --- | --- | --- |
| 400 | `invalid_json` | The body is not valid JSON, is empty, has unknown fields or has trailing data. |
| 400 | `body_too_large` | The body is larger than 1 MiB. |
| 400 | `invalid_path`, `invalid_query` | A path or query value is not a valid number. |
| 400 | `invalid_idempotency_key` | The key is longer than 128 characters. |
| 404 | `route_not_found` | No route matches the method and path. |
| 404 | `<entity>_not_found` | For example `product_not_found`, `order_not_found`, `location_not_found`. |
| 409 | `invalid_transition` | The document status does not allow this action. |
| 409 | `insufficient_stock` | Not enough free stock for the action. |
| 409 | `insufficient_cost_layers` | Cost layers hold less than the stock. This means that data is inconsistent. |
| 409 | `location_count_open` | A cycle count blocks the location. |
| 409 | `count_already_open` | The location already has an open count. |
| 409 | `capacity_exceeded` | The movement puts the destination location above its capacity. |
| 409 | `duplicate` | A unique value (SKU, code, order number, reference) already exists. |
| 409 | `lot_expiry_mismatch` | The lot exists with a different expiry date. |
| 409 | `idempotency_in_progress` | A request with the same key is still running. |
| 422 | `validation_failed` | A field is missing or has a bad value. |
| 422 | `unknown_unit` | The product has no such unit. |
| 422 | `lot_required`, `lot_not_allowed`, `expiry_required` | The lot data does not match the tracking mode. |
| 422 | `over_receipt` | The receive puts the line above expected plus tolerance. |
| 422 | `under_receipt` | Lines are short and `accept_short` is not set. |
| 422 | `transfer_over_receipt` | More units received than were shipped. |
| 422 | `uncounted_lines` | A count line has no counted quantity. |
| 422 | `wrong_location_type`, `wrong_warehouse` | The location does not fit the action. |
| 422 | `quantity_overflow` | A quantity or amount does not fit in 64 bits. |
| 422 | `idempotency_key_reused` | The key was used with a different body. |
| 500 | `internal` | An unexpected error. The server log has the details. |
| 503 | `database_unavailable` | `/healthz` cannot reach MySQL. |

## Project layout

```text
cmd/warehouse/        main: serve and migrate commands, graceful shutdown
internal/config/      environment variables and the optional .env file
internal/domain/      pure business rules: entities, state machines, unit conversion,
                      FEFO/FIFO allocation, FIFO costing, receipt tolerance
internal/store/       MySQL repository with database/sql, transactions with retry
internal/service/     use cases, each in one transaction with row locks
internal/httpapi/     routes, JSON handlers, error mapping, idempotency middleware
internal/migrate/     migration runner with schema_migrations and an advisory lock
internal/testdb/      creates and drops *_test databases for integration tests
migrations/           numbered SQL files, embedded into the binary
```

The domain package does not import the database or HTTP packages. The service package is the only place that combines the store and the domain rules.

## Current limitations

- There is no authentication or authorization. Put the service behind a gateway that handles it.
- A pick always takes the full allocation. Short picks (the bin has less than the system says) are not supported. Use a cycle count to correct the bin first.
- An order ships in one shipment. Partial shipments of one order are not supported. Backorders cover stock that was short at allocation.
- Allocation is all-or-backorder at the time of the call. There is no wave planning or pick path optimization beyond sorting by location code.
- A transfer ships in full or not at all. Partial transfer shipments are not supported.
- Transfer receipt quantities are in base units. Lost units on a transfer are written off. There is no claim workflow.
- Cost layers are per warehouse. There is no standard or average costing, no landed cost and no currency other than IDR.
- A count gain takes the latest known unit cost, or 0 when the product has no cost history in that warehouse.
- Order, receipt and transfer lines cannot change after creation.
- The idempotency record is written after the business transaction commits. If the process stops between the two steps, the key stays "in progress". Delete the row from `idempotency_keys` to free it. Old keys are not removed automatically.
- FIFO uses the oldest receipt time in a stock row. When stock from two receipts goes into one bin and lot, the row keeps the older time.
- Migrations only go up. There are no down migrations.
