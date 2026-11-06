package applications

import (
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type fakeEngine struct {
	books port.IOrderBookRepository
	t     *testing.T

	calls int
	order domain.Order
	match func(order domain.Order) (port.MatchResult, error)
}

// Match mirrors the real engine contract: the book change is committed before
// the result is returned to PlaceOrder.
func (f *fakeEngine) Match(order domain.Order) (port.MatchResult, error) {
	f.calls++
	f.order = order

	var result port.MatchResult
	var err error
	if f.match != nil {
		result, err = f.match(order)
	}
	if err != nil {
		return result, err
	}

	f.commit(order, result)
	return result, nil
}

func (f *fakeEngine) commit(order domain.Order, result port.MatchResult) {
	if f.books == nil {
		return
	}

	now := time.Now().UTC()
	change := port.OrderBookChange{}

	for i := range result.AffectedOrders {
		affected := result.AffectedOrders[i]
		affected.UpdatedAt = now

		if affected.Qty <= 0 {
			change.Removes = append(change.Removes, affected.ID)
			continue
		}
		change.Upserts = append(change.Upserts, affected)
	}

	remaining := order.Qty - result.FilledQty
	if remaining > 0 {
		incoming := order
		incoming.Qty = remaining
		incoming.Status = domain.OrderStatusNew
		if result.FilledQty > 0 {
			incoming.Status = domain.OrderStatusPartiallyMatched
		}
		incoming.UpdatedAt = now
		change.Upserts = append(change.Upserts, incoming)
	}

	if len(change.Removes) == 0 && len(change.Upserts) == 0 {
		return
	}
	if err := f.books.Apply(order.Stock, change); err != nil && f.t != nil {
		f.t.Fatalf("fake engine apply book: %v", err)
	}
}

type fakePublisher struct {
	events []port.Event
	err    error
}

func (f *fakePublisher) Publish(event port.Event) error {
	f.events = append(f.events, event)
	return f.err
}

type placeOrderFixture struct {
	uc        *PlaceOrder
	stocks    port.IStockRepository
	orders    port.IOrderRepository
	books     port.IOrderBookRepository
	trades    port.ITradeRepository
	engine    *fakeEngine
	publisher *fakePublisher
}

func newPlaceOrderFixture(t *testing.T) *placeOrderFixture {
	t.Helper()

	stocks := memdb.NewStockRepo()
	orders := memdb.NewOrderRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	engine := &fakeEngine{books: books, t: t}
	publisher := &fakePublisher{}

	return &placeOrderFixture{
		uc:        NewPlaceOrder(stocks, orders, trades, engine, publisher),
		stocks:    stocks,
		orders:    orders,
		books:     books,
		trades:    trades,
		engine:    engine,
		publisher: publisher,
	}
}

func seedStock(t *testing.T, repo port.IStockRepository, code string) domain.Stock {
	t.Helper()

	stock := domain.Stock{ID: ulid.Make(), Code: code}
	if err := repo.Save(&stock); err != nil {
		t.Fatalf("seed stock: %v", err)
	}

	return stock
}

func makeOrder(
	stock domain.Stock,
	userID string,
	side domain.OrderSideType,
	status domain.OrderStatusType,
	price string,
	qty int,
	createdAt time.Time,
) domain.Order {
	return domain.Order{
		ID:        ulid.Make(),
		UserID:    userID,
		Stock:     stock,
		Side:      side,
		Status:    status,
		Price:     decimal.RequireFromString(price),
		Qty:       qty,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func makeTrade(stock domain.Stock, buyOrder, sellOrder domain.Order, price string, quantity int, createdAt time.Time) domain.Trade {
	return domain.Trade{
		ID:        ulid.Make(),
		Stock:     stock,
		Price:     domain.Price(decimal.RequireFromString(price)),
		Quantity:  quantity,
		BuyOrder:  buyOrder,
		SellOrder: sellOrder,
		CreatedAt: createdAt,
	}
}
