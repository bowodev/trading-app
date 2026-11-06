package applications

import (
	"time"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/shopspring/decimal"
)

type Page[T any] struct {
	NextPointer string
	Limit       int
	Data        []T
}

type OrderView struct {
	ID        string
	UserID    string
	StockCode string
	Side      domain.OrderSideType
	Status    domain.OrderStatusType
	Price     string
	Qty       int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type TradeView struct {
	ID          string
	StockCode   string
	Price       string
	Quantity    int
	BuyOrderID  string
	SellOrderID string
	CreatedAt   time.Time
}

type PriceView struct {
	Last   string
	Change string
	Volume int
}

type OrderBookLevelView struct {
	Price string
	Qty   int
}

type OrderBookView struct {
	Bids []OrderBookLevelView
	Asks []OrderBookLevelView
}

type SnapshotView struct {
	StockCode string
	Price     PriceView
	OrderBook OrderBookView
	Trades    []TradeView
}

func newOrderView(order domain.Order) OrderView {
	return OrderView{
		ID:        order.ID.String(),
		UserID:    order.UserID,
		StockCode: order.Stock.Code,
		Side:      order.Side,
		Status:    order.Status,
		Price:     order.Price.String(),
		Qty:       order.Qty,
		CreatedAt: order.CreatedAt,
		UpdatedAt: order.UpdatedAt,
	}
}

func newTradeView(trade domain.Trade) TradeView {
	return TradeView{
		ID:          trade.ID.String(),
		StockCode:   trade.Stock.Code,
		Price:       decimal.Decimal(trade.Price).String(),
		Quantity:    trade.Quantity,
		BuyOrderID:  trade.BuyOrder.ID.String(),
		SellOrderID: trade.SellOrder.ID.String(),
		CreatedAt:   trade.CreatedAt,
	}
}

func newTradeViews(trades []domain.Trade) []TradeView {
	views := make([]TradeView, 0, len(trades))
	for _, trade := range trades {
		views = append(views, newTradeView(trade))
	}

	return views
}
