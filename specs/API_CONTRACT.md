# API Contract

## Create Order

- endpoint: `[POST] /api/v1/orders`
- identity: optional `X-User-ID` header (fallback: `userId` in the body).
  Missing identity generates `anonymous-<ULID>`, so the minimal payload below
  works and two anonymous orders can still match each other.

### Request
```json
{
    "stockCode": "BMRI",
    "side": "SELL", // BUY || SELL
    "price": "1000", // Decimal
    "qty": 1 // integer
}
```
### Response

#### Success (201 - Created)
```json
{
    "message": "order placed",
    "data": {
        "order": {
            "id": "01J...",
            "userId": "user-1",
            "stockCode": "BMRI",
            "side": "SELL",
            "status": "NEW",
            "price": "1000",
            "qty": 1,
            "createdAt": "2026-11-06T08:00:00Z",
            "updatedAt": "2026-11-06T08:00:00Z"
        },
        "trades": []
    }
}
```

## Get Order List

Get order list using cursor pagination

- endpoint: `[GET] /api/v1/orders`
- identity: optional `X-User-ID` header (fallback: `userId` query param).
  When identity is omitted the endpoint returns orders from every user.

### Request (query param)
```
userId    // optional; scopes the list to one user
stockCode
status    // NEW || PARTIALLY_MATCHED || MATCHED
cursor    // nextPointer from the previous page
limit     // default 10, max 100
```

### Response (200 - OK)
```json
{
    "message": "success",
    "data":{
        "nextPointer":"id",
        "limit": 10,
        "data":[]
    }
}
```

## Get Trade History

Get trade history using cursor pagination

- endpoint: `[GET] /api/v1/orders/history`
- identity: optional `X-User-ID` header (fallback: `userId` query param).
  When identity is omitted the endpoint returns every executed trade.

### Request (query param)
```
userId    // optional; scopes history to trades involving one user
cursor    // nextPointer from the previous page
limit     // default 10, max 100
```

### Response (200 - OK)
```json
{
    "message": "success",
    "data":{
        "nextPointer":"id",
        "limit": 10,
        "data":[]
    }
}
```

## Get Snapshots

- endpoint: `[GET] /api/v1/snapshots`

### Request

query parameters
```
stockCode //string
showTradeLimit // number
```

The snapshot reads the in-memory market data. Startup seeds `last` and
`previous close` for every configured stock, and the periodic price simulator
keeps moving `last` until a real trade replaces it.

### Response

### Success (200 - Ok)

```json
{
    "message": "success",
    "data": {
        "stockCode": "BMRI",
        "price": {
            "last": "23000",
            "change": "20",
            "volume": 1800,
        },
        "orderBook": {
            "bids": [{ "price": "999", "qty": 500 }, { "price": "998", "qty": 1200 }],
            "asks": [{ "price": "1000", "qty": 300 }, { "price": "1001", "qty": 800 }]
        },
        "trades": []
    }
}
```

## Errors

```json
{
    "message": "invalid argument"
}
```

| Status | Meaning |
|--------|---------|
| 400 | invalid request body or query |
| 404 | stock not found |
| 405 | method not allowed |
| 500 | internal or matching error |

## Health Check

- endpoint: `[GET] /healthz`

```json
{
    "message": "ok"
}
```

## WebSocket

- endpoint: `ws://localhost:8080/api/v1/ws`
- optional query: `userId` — default identity for the `order.update` channel
- The WebSocket runs in the same process and port as the REST API so it shares
  the in-memory order book.
- There is no implicit subscription on connect. Clients send `subscribe` and
  `unsubscribe` messages.

### Channels

| Channel | Payload | Scope |
|---|---|---|
| `market.ticker` | last price, change, cumulative volume | per stock |
| `market.trade` | executed trade | per stock |
| `market.orderbook` | aggregated bid/ask depth | per stock |
| `order.update` | order status and quantity | per stock and per user |

### Subscribe

```json
{
    "type": "subscribe",
    "channels": ["market.ticker", "market.trade", "market.orderbook"],
    "stockCode": "BMRI"
}
```

- `channels` may also be sent as a single `channel` string.
- `stockCode` is optional and comma-separated (`BMRI,BBCA`). Omitting it
  subscribes to every stock on those channels.
- `order.update` requires an identity: send `userId` in the subscribe message or
  use the `?userId=` query parameter.

Server acknowledgement:

```json
{
    "type": "subscribed",
    "data": {
        "channels": ["market.ticker", "market.trade", "market.orderbook"],
        "stockCode": "BMRI"
    }
}
```

After the acknowledgement, `market.ticker` and `market.orderbook`
subscriptions immediately receive the current state for each requested stock.
`market.trade` and `order.update` are live-only streams.

### Unsubscribe

```json
{
    "type": "unsubscribe",
    "channels": ["market.trade"],
    "stockCode": "BMRI"
}
```

Omit `stockCode` to remove the whole channel subscription.

### market.ticker

Initial message on subscribe, then one message per executed trade:

```json
{
    "type": "market.ticker",
    "stockCode": "BMRI",
    "data": {
        "stockCode": "BMRI",
        "last": "1000",
        "change": "5",
        "volume": 1825
    }
}
```

### market.trade

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

### market.orderbook

Initial message on subscribe, then refreshed after every order or trade that
changes the book:

```json
{
    "type": "market.orderbook",
    "stockCode": "BMRI",
    "data": {
        "stockCode": "BMRI",
        "bids": [{ "price": "999", "qty": 200 }],
        "asks": [{ "price": "1001", "qty": 300 }]
    }
}
```

Depth is capped at 10 levels per side.

### order.update

Sent for the order that was just placed and for resting orders whose status or
quantity changed after a match. Clients only receive orders belonging to the
user they subscribed as.

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

### Errors

```json
{
    "type": "error",
    "stockCode": "NONE",
    "message": "stock not found"
}
```

| Message | Meaning |
|---|---|
| `unknown channel "..."` | channel name is not supported |
| `at least one channel is required` | subscribe/unsubscribe had no channels |
| `userId is required for order.update` | `order.update` needs an identity |
| `stock not found` | `stockCode` does not exist |
| `text messages only` | binary frames are not accepted |

A full book refresh can also be fetched with `GET /api/v1/snapshots`.