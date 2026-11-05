package domain

import (
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type (
	OrderSideType   string
	OrderStatusType string
)

const (
	OrderSideSell OrderSideType = "SELL"
	OrderSideBuy  OrderSideType = "BUY"
)

const (
	OrderStatusNew              OrderStatusType = "NEW"
	OrderStatusPartiallyMatched OrderStatusType = "PARTIALLY_MATCHED"
	OrderStatusMatch            OrderStatusType = "MATCHED"
)

type Order struct {
	ID     ulid.ULID
	UserID string
	Stock  Stock
	Side   OrderSideType
	Status OrderStatusType
	Price  decimal.Decimal
	Qty    int

	CreatedAt time.Time
	UpdatedAt time.Time
}
