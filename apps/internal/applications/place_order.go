package applications

import (
	"errors"
	"fmt"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type PlaceOrderCommand struct {
	UserID    string
	StockCode string
	Side      domain.OrderSideType
	Price     decimal.Decimal
	Qty       int
}

type PlaceOrderResult struct {
	Order  OrderView
	Trades []TradeView
}

type PlaceOrder struct {
	stocks    port.IStockRepository
	orders    port.IOrderRepository
	trades    port.ITradeRepository
	engine    port.IMatchingEngine
	publisher port.IEventPublisher
}

func NewPlaceOrder(
	stocks port.IStockRepository,
	orders port.IOrderRepository,
	trades port.ITradeRepository,
	engine port.IMatchingEngine,
	publisher port.IEventPublisher,
) *PlaceOrder {
	return &PlaceOrder{
		stocks:    stocks,
		orders:    orders,
		trades:    trades,
		engine:    engine,
		publisher: publisher,
	}
}

func (uc *PlaceOrder) Execute(cmd PlaceOrderCommand) (PlaceOrderResult, error) {
	if err := cmd.validate(); err != nil {
		return PlaceOrderResult{}, err
	}

	stock, err := uc.stocks.FindByCode(cmd.StockCode)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return PlaceOrderResult{}, ErrStockNotFound
		}
		return PlaceOrderResult{}, err
	}

	now := time.Now().UTC()
	order := domain.Order{
		ID:        ulid.Make(),
		UserID:    cmd.UserID,
		Stock:     *stock,
		Side:      cmd.Side,
		Status:    domain.OrderStatusNew,
		Price:     cmd.Price,
		Qty:       cmd.Qty,
		CreatedAt: now,
		UpdatedAt: now,
	}

	match, err := uc.engine.Match(order)
	if err != nil {
		return PlaceOrderResult{}, fmt.Errorf("%w: %v", ErrMatchingFailed, err)
	}
	if match.FilledQty < 0 || match.FilledQty > cmd.Qty {
		return PlaceOrderResult{}, fmt.Errorf("%w: invalid filled qty %d", ErrMatchingFailed, match.FilledQty)
	}

	remaining := cmd.Qty - match.FilledQty
	switch {
	case match.FilledQty == 0:
		order.Status = domain.OrderStatusNew
	case remaining == 0:
		order.Status = domain.OrderStatusMatch
	default:
		order.Status = domain.OrderStatusPartiallyMatched
		order.Qty = remaining
	}

	if err := uc.orders.Save(&order); err != nil {
		return PlaceOrderResult{}, err
	}

	for i := range match.Trades {
		if err := uc.trades.Save(&match.Trades[i]); err != nil {
			return PlaceOrderResult{}, err
		}
	}

	for i := range match.AffectedOrders {
		match.AffectedOrders[i].UpdatedAt = now
		if err := uc.orders.Update(&match.AffectedOrders[i]); err != nil {
			return PlaceOrderResult{}, err
		}
	}

	publishOrderPlaced(uc.publisher, order)
	for _, affected := range match.AffectedOrders {
		publishOrderUpdated(uc.publisher, affected)
	}
	publishTrades(uc.publisher, match.Trades)

	return PlaceOrderResult{
		Order:  newOrderView(order),
		Trades: newTradeViews(match.Trades),
	}, nil
}

func (cmd PlaceOrderCommand) validate() error {
	if cmd.UserID == "" || cmd.StockCode == "" {
		return ErrInvalidArgument
	}
	if cmd.Side != domain.OrderSideBuy && cmd.Side != domain.OrderSideSell {
		return ErrInvalidArgument
	}
	if !cmd.Price.IsPositive() {
		return ErrInvalidArgument
	}
	if cmd.Qty <= 0 {
		return ErrInvalidArgument
	}

	return nil
}
