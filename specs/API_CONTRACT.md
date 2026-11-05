# API Contract

## Create Order

- endpoint: `[POST] /api/v1/orders`

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

#### Success (204 - Created)
```json
{
    "message": "your order has been queued"
}
```

## Get Order List

Get order list using cursor pagination

- endpoint: `[GET] /api/v1/orders`

### Request (query param)
```
stockCode
status
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