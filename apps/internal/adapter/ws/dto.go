package ws

import (
	"time"

	"github.com/bowodev/trading-backend/apps/internal/applications"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

// The channel name doubles as the message type.
const (
	ChannelMarketTicker    = "market.ticker"
	ChannelMarketTrade     = "market.trade"
	ChannelMarketOrderBook = "market.orderbook"
	ChannelOrderUpdate     = "order.update"
)

// Message is the envelope sent to clients.
type Message struct {
	Type      string `json:"type"`
	StockCode string `json:"stockCode,omitempty"`
	Data      any    `json:"data,omitempty"`
	Message   string `json:"message,omitempty"`
}

// clientCommand is the envelope sent by clients.
type clientCommand struct {
	Type      string   `json:"type"`
	Channel   string   `json:"channel,omitempty"`
	Channels  []string `json:"channels,omitempty"`
	StockCode string   `json:"stockCode,omitempty"`
	UserID    string   `json:"userId,omitempty"`
}

type subscriptionPayload struct {
	Channels  []string `json:"channels"`
	StockCode string   `json:"stockCode,omitempty"`
	UserID    string   `json:"userId,omitempty"`
}

type orderPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	StockCode string    `json:"stockCode"`
	Side      string    `json:"side"`
	Status    string    `json:"status"`
	Price     string    `json:"price"`
	Qty       int       `json:"qty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type tradePayload struct {
	ID          string    `json:"id"`
	StockCode   string    `json:"stockCode"`
	Price       string    `json:"price"`
	Quantity    int       `json:"quantity"`
	BuyOrderID  string    `json:"buyOrderId"`
	SellOrderID string    `json:"sellOrderId"`
	CreatedAt   time.Time `json:"createdAt"`
}

type tickerPayload struct {
	StockCode string `json:"stockCode"`
	Last      string `json:"last"`
	Change    string `json:"change"`
	Volume    int    `json:"volume"`
}

type orderBookLevelPayload struct {
	Price string `json:"price"`
	Qty   int    `json:"qty"`
}

type orderBookEventPayload struct {
	StockCode string                  `json:"stockCode"`
	Bids      []orderBookLevelPayload `json:"bids"`
	Asks      []orderBookLevelPayload `json:"asks"`
}

func newOrderPayload(order domain.Order) orderPayload {
	return orderPayload{
		ID:        order.ID.String(),
		UserID:    order.UserID,
		StockCode: order.Stock.Code,
		Side:      string(order.Side),
		Status:    string(order.Status),
		Price:     order.Price.String(),
		Qty:       order.Qty,
		CreatedAt: order.CreatedAt,
		UpdatedAt: order.UpdatedAt,
	}
}

func newTradePayload(trade domain.Trade) tradePayload {
	return tradePayload{
		ID:          trade.ID.String(),
		StockCode:   trade.Stock.Code,
		Price:       decimal.Decimal(trade.Price).String(),
		Quantity:    trade.Quantity,
		BuyOrderID:  trade.BuyOrder.ID.String(),
		SellOrderID: trade.SellOrder.ID.String(),
		CreatedAt:   trade.CreatedAt,
	}
}

func newTickerPayload(stockCode string, price applications.PriceView) tickerPayload {
	return tickerPayload{
		StockCode: stockCode,
		Last:      price.Last,
		Change:    price.Change,
		Volume:    price.Volume,
	}
}

func newOrderUpdateMessage(order domain.Order) Message {
	return Message{
		Type:      ChannelOrderUpdate,
		StockCode: order.Stock.Code,
		Data:      newOrderPayload(order),
	}
}

func newTradeMessage(trade domain.Trade) Message {
	return Message{
		Type:      ChannelMarketTrade,
		StockCode: trade.Stock.Code,
		Data:      newTradePayload(trade),
	}
}

func newTickerMessage(stockCode string, price applications.PriceView) Message {
	return Message{
		Type:      ChannelMarketTicker,
		StockCode: stockCode,
		Data:      newTickerPayload(stockCode, price),
	}
}

func newMarketTickerMessage(event port.MarketTickerEvent) Message {
	return Message{
		Type:      ChannelMarketTicker,
		StockCode: event.StockCode,
		Data: tickerPayload{
			StockCode: event.StockCode,
			Last:      event.Last.String(),
			Change:    event.Change.String(),
			Volume:    event.Volume,
		},
	}
}

func newOrderBookMessage(stockCode string, view applications.OrderBookView) Message {
	return Message{
		Type:      ChannelMarketOrderBook,
		StockCode: stockCode,
		Data: orderBookEventPayload{
			StockCode: stockCode,
			Bids:      newLevelPayloads(view.Bids),
			Asks:      newLevelPayloads(view.Asks),
		},
	}
}

func newLevelPayloads(views []applications.OrderBookLevelView) []orderBookLevelPayload {
	payloads := make([]orderBookLevelPayload, 0, len(views))
	for _, view := range views {
		payloads = append(payloads, orderBookLevelPayload{
			Price: view.Price,
			Qty:   view.Qty,
		})
	}

	return payloads
}
