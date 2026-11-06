package port

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/shopspring/decimal"
)

type Event interface {
	EventName() string
}

// OrderPlacedEvent reports a newly accepted order. It is delivered on the
// order.update channel together with OrderUpdatedEvent.
type OrderPlacedEvent struct {
	Order domain.Order
}

func (OrderPlacedEvent) EventName() string {
	return "order.update"
}

// OrderUpdatedEvent reports a status or quantity change for an order that was
// already known to the system, such as a resting order hit by a match.
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

// MarketTickerEvent carries the last price and cumulative volume for a stock
// after a trade.
type MarketTickerEvent struct {
	StockCode string
	Last      decimal.Decimal
	Change    decimal.Decimal
	Volume    int
}

func (MarketTickerEvent) EventName() string {
	return "market.ticker"
}

// Publish is best-effort: callers do not roll back completed work when a
// publish fails.
type IEventPublisher interface {
	Publish(event Event) error
}
