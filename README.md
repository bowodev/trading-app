# Mini Exchange — Realtime Trading Backend

A small realtime trading backend written in Go. It exposes a REST API, a
WebSocket stream, an in-memory matching engine, and a price simulator. The
project is intentionally dependency-light: no database, message broker, or
external market-data provider is required to run it.

## Features

- `POST /api/v1/orders` — submit BUY/SELL orders with concurrent-safe matching.
- `GET /api/v1/orders` — cursor-paginated order list, filterable by stock and
  status (and optionally by user).
- `GET /api/v1/orders/history` — cursor-paginated executed trades.
- `GET /api/v1/snapshots` — last price, change, volume, order-book depth, and
  recent trades for one stock.
- `GET /healthz` — liveness check.
- WebSocket `/api/v1/ws` with `market.ticker`, `market.trade`,
  `market.orderbook`, and `order.update`.
- Price-time-priority matching engine with partial fills and FIFO priority.
- Periodic price simulation so clients get a useful snapshot before the first
  trade.

---

## Quick start

Requirements: Go 1.27+ (the exact version is declared in `go.mod`).

```bash
go run ./apps/cmd/api
```

The API listens on `:8080` by default.

```bash
curl http://localhost:8080/healthz
# {"message":"ok"}
```

### Environment variables

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP/WebSocket port. |
| `STOCK_CODES` | `BMRI,BBCA,BBRI,TLKM` | Comma-separated stock universe. |
| `WS_ALLOWED_ORIGINS` | `*` | Comma-separated origin host patterns allowed by the WebSocket upgrader. The default accepts any origin, which keeps desktop clients (Bruno, Postman) working; set an explicit list such as `localhost:3000,app.example.com` to restrict browser access. |
| `PRICE_SIM_ENABLED` | `true` | Set to `false`, `0`, `no`, or `off` to stop the periodic price walk. Seed prices are still written. |
| `PRICE_SIM_INTERVAL` | `3s` | How often the simulator moves each price. Any Go duration string. |
| `PRICE_SIM_VOLATILITY_BPS` | `40` | Maximum simulated move per tick in basis points (`40` = 0.40%). |

### End-to-end curl example

```bash
# 1. A seller rests an ask at 1000.
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -H 'X-User-ID: seller' \
  -d '{"stockCode":"BMRI","side":"SELL","price":"1000","qty":10}'

# 2. A buyer crosses the ask; the response includes the executed trade.
curl -X POST http://localhost:8080/api/v1/orders \
  -H 'Content-Type: application/json' \
  -H 'X-User-ID: buyer' \
  -d '{"stockCode":"BMRI","side":"BUY","price":"1000","qty":10}'

# 3. Read orders (omit X-User-ID to see every user's orders).
curl 'http://localhost:8080/api/v1/orders?stockCode=BMRI&status=MATCHED'

# 4. Read trade history (omit X-User-ID to see every trade).
curl 'http://localhost:8080/api/v1/orders/history'

# 5. Read the market snapshot.
curl 'http://localhost:8080/api/v1/snapshots?stockCode=BMRI&showTradeLimit=20'
```

Identity is optional. If no `X-User-ID` header (or `userId` body/query field)
is supplied:

- create order uses a generated identity such as `anonymous-01J...`;
- order list and trade history return data for every user.

When an identity is supplied, list and history are scoped to that user. The
WebSocket `order.update` channel still requires an explicit identity.

Full request/response details are in
[`specs/API_CONTRACT.md`](specs/API_CONTRACT.md).

---

## Architecture

The project follows a ports-and-adapters (clean architecture) layout:

```text
                 ┌──────────────────────────────┐
HTTP clients ───▶│ adapter/http                 │
                 │  decode, validate, present   │
                 └──────────────┬───────────────┘
                                │
                 ┌──────────────▼───────────────┐
                 │ applications (use cases)     │
                 │  PlaceOrder, ListOrders,     │
                 │  TradeHistory, Snapshot,     │
                 │  TickerRecorder, Simulator   │
                 └──────┬───────────────┬───────┘
                        │               │
                 ┌──────▼──────┐  ┌─────▼────────────┐
                 │ port        │  │ domain           │
                 │ interfaces  │  │ Order, Trade,    │
                 │             │  │ Stock, Book      │
                 └──────┬──────┘  └──────────────────┘
                        │
        ┌───────────────┼────────────────┬──────────────────┐
        │               │                │                  │
┌───────▼──────┐ ┌──────▼───────┐ ┌──────▼───────┐ ┌────────▼───────┐
│ adapter/     │ │ adapter/     │ │ adapter/     │ │ adapter/ws     │
│ memdb        │ │ matching_    │ │ http         │ │ hub + clients  │
│ repositories │ │ engine       │ │ presenter    │ │ pub/sub        │
└──────────────┘ └──────────────┘ └──────────────┘ └────────────────┘
                                         ▲
                                         │
                                  WebSocket clients
```

### Directory layout

```text
apps/
├── cmd/api/main.go                 # composition root, env config, shutdown
└── internal/
    ├── domain/                     # entities and value types
    ├── port/                       # repository, engine, event interfaces
    ├── applications/               # use cases and price simulator
    └── adapter/
        ├── http/                   # REST handlers + DTOs
        ├── ws/                     # WebSocket hub, subscriptions, DTOs
        ├── memdb/                  # in-memory repositories
        └── matching_engine/        # serialized matching engine
specs/API_CONTRACT.md               # REST + WebSocket contract
```

Dependencies point inward: `domain` has no imports from adapters, and
`applications` only depends on `port` and `domain`. Swapping the in-memory
repositories for PostgreSQL/Redis means implementing the same port interfaces.

---

## System flow

### Placing an order

1. HTTP adapter decodes and validates JSON, resolves the optional identity, and
   parses a decimal price.
2. `PlaceOrder` loads the stock and builds a `NEW` order.
3. `matching_engine.Engine.Match` sends the order to a single engine goroutine
   through a buffered request channel and waits for the result.
4. The engine scans the opposite book in price-time order, skips self-trades,
   and produces trades plus affected resting orders.
5. `Engine.commit` applies all removals and updates in one
   `IOrderBookRepository.Apply` call. The book is replaced only if every change
   succeeds.
6. `PlaceOrder` persists the incoming order, the trades, and the affected
   resting orders, then publishes `order.update`, `market.trade`, and
   `market.ticker` events.
7. `TickerRecorder` updates last price and cumulative volume.
8. The publisher fan-out writes an application log and forwards events to the
   WebSocket hub.

### Reading a snapshot

`GET /api/v1/snapshots` reads the stock, market data, recent trades, and one
consistent bid/ask snapshot. Price, depth, and trades are returned together so
a client can render its first screen without waiting for a WebSocket event.

### WebSocket subscription

1. The client connects; nothing is subscribed implicitly.
2. The client sends `subscribe` with one or more channels and an optional
   comma-separated `stockCode` list.
3. The server acknowledges and sends the current ticker/order-book state for
   the requested stocks.
4. Subsequent trade, ticker, order-book, and order-status events are pushed
   from the hub.

---

## Concurrency model and race-condition analysis

### Matching is single-writer

All matching requests go through one goroutine (`Engine.loop`) reading a
buffered channel of size 64. That serializes check-and-commit for every order,
which is the core guarantee against double fills:

- only one goroutine reads or writes the order book during matching;
- `Match` returns only after the book change has been committed;
- two simultaneous BUY orders cannot both consume the same resting SELL,
  because the second order sees the book after the first commit.

Repository methods still use `sync.RWMutex` because HTTP readers, the
WebSocket snapshot path, and the engine access them concurrently. Reads return
deep copies (`cloneOrderBook`, `Snapshot`, `BestBids`, `BestAsks`) so callers
cannot mutate repository state.

`OrderBookRepo.Apply` clones the book, applies all removes/upserts to the
clone, and stores it only on success. A failed remove or invalid upsert leaves
the previous book untouched.

Run the race detector:

```bash
go test -race ./...
```

### Consistency boundary

The in-memory implementation is deliberately simple:

- the book commit happens before order/trade persistence;
- in-memory `Save`/`Update` only fail on invalid data, so a mid-flow failure is
  practically unreachable in this build;
- publishing is best-effort and never rolls back a completed match.

For a production system, the book mutation, order rows, and trade rows would be
committed in one database transaction (or an outbox + idempotent consumer)
before the event is published. The current split is documented as a known
limitation rather than hidden.

### Other shared state

- Order, trade, stock, and market-data repositories each own an `RWMutex`.
- `marketDataRepo.SetLast` updates only the last price and timestamp, so the
  simulator cannot lose a concurrent trade's volume increment.
- The WebSocket hub protects its client map with an `RWMutex`; each client
  protects its subscription map with its own `RWMutex`.
- The price simulator runs in exactly one goroutine and owns its random source.

---

## Non-blocking WebSocket broadcast

Each connected client has a buffered send channel (capacity 64). Broadcasting:

1. marshals the message once;
2. iterates the client map under a read lock;
3. checks the client's channel/stock/user subscription;
4. performs a non-blocking send:

```go
select {
case client.send <- payload:
default:
    // slow client: drop this frame, then detach the client
}
```

A client whose buffer is full is removed outside the publisher path and its
connection is closed. Order placement never waits for a socket write. The
trade-off is intentional: a slow consumer loses frames instead of growing
memory without bound or blocking the matching path.

There is no goroutine-per-message. Each connection has one read loop and one
write loop; writes use a 5-second timeout. Slow-client removal is synchronous
after the broadcast read lock is released, so an event burst cannot spawn an
unbounded number of cleanup goroutines.

---

## Realtime price simulation

The simulator makes the system useful even when no external feed is connected:

1. At startup, `PriceSimulator.Seed()` writes `last` and `previous close` for
   every configured stock. Known codes use realistic base prices; unknown codes
   use `1000`.
2. Every `PRICE_SIM_INTERVAL` (default `3s`), each stock moves by a random
   amount in `[-PRICE_SIM_VOLATILITY_BPS, +PRICE_SIM_VOLATILITY_BPS]`
   (default `±40 bps`), rounded to two decimals.
3. The new price is written atomically with `SetLast`, then a `market.ticker`
   event is published with `last`, `change` (`last - previous close`), and
   cumulative volume.
4. When a real trade executes, `TickerRecorder` overwrites the last price with
   the trade price and increments volume. A trade therefore wins over a
   simulated tick.

`change` is meaningful from the first snapshot because previous close is
seeded. To run with static prices:

```bash
PRICE_SIM_ENABLED=false go run ./apps/cmd/api
```

The simulator is internal by design. The `port.IEventPublisher` and repository
interfaces make it possible to replace it with Finnhub, Binance, Kraken, or
Alpaca without changing the matching or WebSocket layers.

---

## Assumptions and capacity model

The design targets the technical-test assumptions:

| Assumption | Design response |
|---|---|
| 1,000 orders/minute (~17/s average, with bursts) | One buffered engine queue and one matching goroutine are comfortably sufficient for this rate. |
| 500 active WebSocket clients | Bounded per-client buffers; O(clients) fan-out per event; no blocking writes. |
| 1–5 subscriptions per client | Subscription map per client with stock filters; `order.update` additionally scoped by user. |
| Broadcast must not block on slow clients | Non-blocking sends with drop-and-detach policy. |

Additional assumptions:

- The stock universe is static and supplied at startup via `STOCK_CODES`.
- All state is in memory and is lost on restart.
- No authentication, rate limiting, or multi-tenant isolation is claimed; the
  optional `X-User-ID` is a caller-supplied identity, not a security boundary.
- One process owns matching. Horizontal scaling is described below as a design
  path, not implemented in this test.
- Order cancellation/amend, idempotency keys, and persistence are out of scope.

---

## Three main bottlenecks and mitigations

### 1. Single matching goroutine (head-of-line blocking)

Every order for every stock enters the same queue. At 1,000 orders/minute this
is well within budget, but a burst or a slow repository call delays unrelated
stocks.

Mitigations:

- shard the engine by stock code (one goroutine/queue per shard, or a
  consistent-hash router) so different stocks match in parallel;
- keep the per-shard critical section small and avoid I/O inside it;
- batch or pipeline persistence outside the matching step;
- add queue-depth and match-latency metrics to detect saturation.

### 2. WebSocket fan-out is O(number of clients)

Every trade can produce trade, ticker, order-book, and order-update messages.
With 500 clients and several subscriptions each, naive fan-out spends CPU on
subscription checks and channel sends.

Mitigations:

- keep the current pattern of marshaling once and sending an immutable byte
  slice;
- index subscribers by `(channel, stockCode)` instead of scanning every client;
- coalesce ticker updates per stock (send latest state at a fixed cadence);
- move fan-out to Redis Pub/Sub, NATS, or Kafka so multiple WebSocket nodes can
  each serve their local clients;
- keep the bounded-buffer drop policy so a slow consumer cannot slow the room.

### 3. In-memory history scans and unbounded memory

Order and trade lists copy every matching row and sort them for cursor
pagination. That is fine for a test, but cost grows with history and the maps
never shrink.

Mitigations:

- maintain secondary indexes by stock/status/user and a time-ordered index;
- keep only a bounded ring buffer for recent in-memory trades and move the full
  history to PostgreSQL or a time-series store;
- add retention/TTL and archive jobs;
- replace full-scan pagination with indexed keyset pagination.

---

## Horizontal scaling strategy

The current binary is a single process. A production deployment would scale in
these steps:

1. **Stateless HTTP layer** behind a load balancer. Any node can accept reads
   and order commands.
2. **Partition matching by stock.** Kafka partitions (or a consistent-hash
   router) send every order for a stock to the same matching worker. A single
   writer per stock preserves FIFO and prevents double fills.
3. **Publish events to a broker.** Matching workers publish `market.*` and
   `order.update` events to Kafka/NATS/Redis. WebSocket nodes subscribe and fan
   out only to their local clients, so client count scales with node count.
4. **Shared read model.** Snapshots and history read from a database/Redis
   cache populated by the event stream. In-memory caches remain per node.
5. **Idempotency and sequencing.** Client order IDs, per-stock sequence
   numbers, and idempotent consumers make retries and failover safe.
6. **Operational controls.** Rate limiting, auth/JWT, metrics, tracing, and
   backpressure alerts are added at the edge.

With this split, 1,000 orders/minute is not the limiting factor; the number of
WebSocket nodes and the event-bus fan-out become the scaling dimensions.

---

## Goroutine leak prevention

- **Matching engine:** `Close` marks the engine closed, closes a stop channel,
  drains queued requests, and waits for `done`. `Close` is idempotent.
- **WebSocket hub:** each connection owns a read loop and a write loop. Closing
  a client closes `done` and the socket, which unblocks both loops. The hub
  tracks handlers with a `WaitGroup` and `Hub.Close` waits for them.
- **No cleanup goroutine per event:** slow clients are removed synchronously
  after the broadcast lock is released.
- **Price simulator:** `Close` closes a stop channel and waits for its single
  goroutine. Calling `Close` before `Start` is safe.
- **HTTP server:** `signal.Notify` triggers `Server.Shutdown` with a 10-second
  timeout, then the deferred engine/hub/simulator closes run.
- All queues and channels are bounded; there is no unbounded `go func()` per
  order or per message.

---

## Data storage

The mandatory implementation is in-memory:

- `orderRepo` — orders by ULID, cursor pagination newest-first;
- `tradeRepo` — trades by ULID, per-stock and per-user listing;
- `orderBookRepo` — bid/ask price levels, atomic `Apply`, deep-copy reads;
- `marketDataRepo` — last price, previous close, volume, timestamp;
- `stockRepo` — stock code to stock metadata.

Redis/Kafka/PostgreSQL are intentionally not required. The port interfaces are
the extension points for those adapters.

---

## REST API summary

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/healthz` | Liveness. |
| `POST` | `/api/v1/orders` | Create a BUY/SELL order. |
| `GET` | `/api/v1/orders` | List orders with cursor pagination. |
| `GET` | `/api/v1/orders/history` | List executed trades with cursor pagination. |
| `GET` | `/api/v1/snapshots` | Ticker, order book, and recent trades for one stock. |

Create order payload (identity optional):

```json
{
  "stockCode": "BMRI",
  "side": "BUY",
  "price": "1000",
  "qty": 10
}
```

Order list / history query parameters:

```text
stockCode   filter by stock (orders only)
status      NEW | PARTIALLY_MATCHED | MATCHED (orders only)
userId      optional user scope
cursor      nextPointer from the previous page
limit       default 10, max 100
```

Error responses use a consistent envelope:

```json
{"message": "invalid argument"}
```

See [`specs/API_CONTRACT.md`](specs/API_CONTRACT.md) for full examples and
status codes.

---

## WebSocket API

### Connect

```text
ws://localhost:8080/api/v1/ws
```

Optional query parameter: `userId` — default identity for `order.update`.

The default configuration accepts any `Origin`, so desktop clients such as Bruno
and Postman connect without extra setup. Set `WS_ALLOWED_ORIGINS` to an explicit
comma-separated host-pattern list to restrict browser access.

There is no implicit subscription. The client sends a command after connecting.

### Subscribe

```json
{
  "type": "subscribe",
  "channels": ["market.ticker", "market.trade", "market.orderbook"],
  "stockCode": "BMRI"
}
```

- `channels` can also be sent as a single `channel` string.
- `stockCode` is optional and comma-separated (`BMRI,BBCA`). Omitting it
  subscribes to every stock on those channels.
- `order.update` requires `userId` in the command or `?userId=` on the URL.

Acknowledgement:

```json
{
  "type": "subscribed",
  "data": {
    "channels": ["market.ticker", "market.trade", "market.orderbook"],
    "stockCode": "BMRI"
  }
}
```

After the acknowledgement, `market.ticker` and `market.orderbook` immediately
send the current state. `market.trade` and `order.update` are live-only.

### Unsubscribe

```json
{"type":"unsubscribe","channels":["market.trade"],"stockCode":"BMRI"}
```

### Channels

| Channel | Payload | Scope |
|---|---|---|
| `market.ticker` | last price, change, cumulative volume | per stock |
| `market.trade` | executed trade | per stock |
| `market.orderbook` | aggregated bid/ask depth (max 10 levels) | per stock |
| `order.update` | order status and remaining quantity | per stock and user |

Example ticker:

```json
{
  "type": "market.ticker",
  "stockCode": "BMRI",
  "data": {
    "stockCode": "BMRI",
    "last": "6000",
    "change": "0",
    "volume": 0
  }
}
```

Example trade:

```json
{
  "type": "market.trade",
  "stockCode": "BMRI",
  "data": {
    "id": "01J...",
    "stockCode": "BMRI",
    "price": "1000",
    "quantity": 10,
    "buyOrderId": "01J...",
    "sellOrderId": "01J...",
    "createdAt": "2026-11-06T08:00:00Z"
  }
}
```

Example order update:

```json
{
  "type": "order.update",
  "stockCode": "BMRI",
  "data": {
    "id": "01J...",
    "userId": "user-1",
    "stockCode": "BMRI",
    "side": "BUY",
    "status": "PARTIALLY_MATCHED",
    "price": "999",
    "qty": 60,
    "createdAt": "2026-11-06T08:00:00Z",
    "updatedAt": "2026-11-06T08:00:10Z"
  }
}
```

Error frames:

```json
{"type":"error","stockCode":"NONE","message":"stock not found"}
```

### Recommended tools and testing

**wscat** (Node.js):

```bash
npx wscat -c ws://localhost:8080/api/v1/ws
```

Then paste:

```json
{"type":"subscribe","channels":["market.ticker","market.trade","market.orderbook"],"stockCode":"BMRI"}
```

**websocat**:

```bash
websocat ws://localhost:8080/api/v1/ws
```

**Postman:** create a new WebSocket request, enter
`ws://localhost:8080/api/v1/ws`, click Connect, then Send the subscribe JSON.
Postman shows incoming frames in the message log.

**Node.js one-liner:**

```bash
node -e '
const ws = new WebSocket("ws://localhost:8080/api/v1/ws");
ws.onopen = () => ws.send(JSON.stringify({
  type: "subscribe",
  channels: ["market.ticker", "market.trade", "market.orderbook"],
  stockCode: "BMRI",
}));
ws.onmessage = (event) => console.log(event.data);
'
```

Use `curl` for the REST endpoints; it does not speak WebSocket.

---

## Testing

```bash
go test ./...
go test -race ./...
```

Coverage includes:

- matching engine price priority, time priority, partial fills, self-trade
  skipping, concurrent no-double-fill, queue backpressure, and shutdown;
- repository atomic `Apply`, deep-copy reads, pagination, and concurrent access;
- application validation, place-order matching, ticker recording, snapshots,
  optional user scoping, and the price simulator;
- HTTP handlers, identity fallback, pagination, filters, and error mapping;
- WebSocket subscribe/unsubscribe, initial snapshots, per-user order updates,
  all-stock subscriptions, and closed-hub behavior.

---

## Bonus items and known limitations

Implemented baseline only. Not implemented (kept as extension points):

- Kafka/NATS or Redis Pub/Sub;
- JWT authentication and rate limiting;
- database persistence and restart recovery;
- metrics/tracing dashboards;
- order cancel/amend and idempotency keys;
- external market-data providers.

The in-memory design is intentional for the technical test. The port
interfaces and event publisher exist so these can be added without rewriting
the domain or matching logic.
