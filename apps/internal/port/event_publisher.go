package port

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/shopspring/decimal"
)

type Event interface {
	EventName() string
}

// OrderPlacedEvent is delivered on order.update.
type OrderPlacedEvent struct {
	Order domain.Order
}

func (OrderPlacedEvent) EventName() string {
	return "order.update"
}

// OrderUpdatedEvent reports a status or quantity change on order.update.
type OrderUpdatedEvent struct {
	Order domain.Order
}

func (OrderUpdatedEvent) EventName() string {
	return "order.update"
}

type TradeExecutedEvent struct {
	Trade domain.Trade
}

func (TradeExecutedEvent) EventName() string {
	return "market.trade"
}

// MarketTickerEvent carries last price and cumulative volume.
type MarketTickerEvent struct {
	StockCode string
	Last      decimal.Decimal
	Change    decimal.Decimal
	Volume    int
}

func (MarketTickerEvent) EventName() string {
	return "market.ticker"
}

// Publish is best-effort; callers do not roll back completed work on failure.
type IEventPublisher interface {
	Publish(event Event) error
}
