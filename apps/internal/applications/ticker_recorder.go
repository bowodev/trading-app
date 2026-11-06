package applications

import (
	"errors"

	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

// TickerRecorder syncs market data from trades so REST and WS see the same numbers.
type TickerRecorder struct {
	marketData port.IMarketDataRepository
	next       port.IEventPublisher
}

func NewTickerRecorder(marketData port.IMarketDataRepository, next port.IEventPublisher) *TickerRecorder {
	return &TickerRecorder{marketData: marketData, next: next}
}

func (r *TickerRecorder) Publish(event port.Event) error {
	trade, ok := event.(port.TradeExecutedEvent)
	if !ok {
		return r.next.Publish(event)
	}

	if err := r.next.Publish(event); err != nil {
		return err
	}

	data, err := r.marketData.Get(trade.Trade.Stock.Code)
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return err
	}
	if errors.Is(err, port.ErrNotFound) {
		data = port.MarketData{
			StockCode: trade.Trade.Stock.Code,
			PrevClose: decimal.Zero,
		}
	}

	data.Last = decimal.Decimal(trade.Trade.Price)
	data.Volume += trade.Trade.Quantity
	data.UpdatedAt = trade.Trade.CreatedAt
	if err := r.marketData.Save(data); err != nil {
		return err
	}

	change := decimal.Zero
	if !data.PrevClose.IsZero() {
		change = data.Last.Sub(data.PrevClose)
	}

	return r.next.Publish(port.MarketTickerEvent{
		StockCode: data.StockCode,
		Last:      data.Last,
		Change:    change,
		Volume:    data.Volume,
	})
}
