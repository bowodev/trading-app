package applications

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
)

func publishOrderPlaced(publisher port.IEventPublisher, order domain.Order) {
	_ = publisher.Publish(port.OrderPlacedEvent{Order: order})
}

func publishOrderUpdated(publisher port.IEventPublisher, order domain.Order) {
	_ = publisher.Publish(port.OrderUpdatedEvent{Order: order})
}

func publishTrades(publisher port.IEventPublisher, trades []domain.Trade) {
	for _, trade := range trades {
		_ = publisher.Publish(port.TradeExecutedEvent{Trade: trade})
	}
}
