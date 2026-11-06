package applications

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/matching_engine"
	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type noopPublisher struct{}

func (noopPublisher) Publish(port.Event) error { return nil }

func TestPlaceOrderNoMatch(t *testing.T) {
	f := newPlaceOrderFixture(t)
	stock := seedStock(t, f.stocks, "BMRI")
	f.publisher.err = errors.New("publisher down")

	result, err := f.uc.Execute(PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Order.Status != domain.OrderStatusNew {
		t.Fatalf("status = %s, want NEW", result.Order.Status)
	}
	if result.Order.Qty != 10 {
		t.Fatalf("qty = %d, want 10", result.Order.Qty)
	}
	if len(result.Trades) != 0 {
		t.Fatalf("trades = %d, want 0", len(result.Trades))
	}

	saved, err := f.orders.FindByID(ulid.MustParse(result.Order.ID))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if saved.UserID != "user-1" || saved.Stock.ID != stock.ID {
		t.Fatalf("saved order mismatch: %+v", saved)
	}

	bids, err := f.books.BestBids(stock.ID, 10)
	if err != nil {
		t.Fatalf("BestBids: %v", err)
	}
	if len(bids) != 1 || bids[0].ID.String() != result.Order.ID {
		t.Fatalf("bids = %+v, want incoming order", bids)
	}

	if len(f.engine.order.UserID) == 0 || f.engine.order.ID.String() != result.Order.ID {
		t.Fatalf("engine received unexpected order: %+v", f.engine.order)
	}
	if len(f.publisher.events) != 1 {
		t.Fatalf("events = %d, want 1", len(f.publisher.events))
	}
	if _, ok := f.publisher.events[0].(port.OrderPlacedEvent); !ok {
		t.Fatalf("event type = %T, want OrderPlacedEvent", f.publisher.events[0])
	}
}

func TestPlaceOrderPartialMatch(t *testing.T) {
	f := newPlaceOrderFixture(t)
	stock := seedStock(t, f.stocks, "BMRI")
	now := time.Now().UTC()

	resting := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusNew, "1000", 6, now)
	if err := f.orders.Save(&resting); err != nil {
		t.Fatalf("save resting order: %v", err)
	}
	if err := f.books.Add(&resting); err != nil {
		t.Fatalf("add resting order to book: %v", err)
	}

	f.engine.match = func(order domain.Order) (port.MatchResult, error) {
		trade := makeTrade(stock, order, resting, "1000", 4, time.Now().UTC())
		affected := resting
		affected.Qty = 2
		affected.Status = domain.OrderStatusPartiallyMatched

		return port.MatchResult{
			FilledQty:      4,
			Trades:         []domain.Trade{trade},
			AffectedOrders: []domain.Order{affected},
		}, nil
	}

	result, err := f.uc.Execute(PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Order.Status != domain.OrderStatusPartiallyMatched {
		t.Fatalf("status = %s, want PARTIALLY_MATCHED", result.Order.Status)
	}
	if result.Order.Qty != 6 {
		t.Fatalf("remaining qty = %d, want 6", result.Order.Qty)
	}
	if len(result.Trades) != 1 || result.Trades[0].Quantity != 4 {
		t.Fatalf("trades = %+v, want one trade of 4", result.Trades)
	}

	book, err := f.books.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	if len(book.Bids["1000"]) != 1 || book.Bids["1000"][0].Qty != 6 {
		t.Fatalf("bids = %+v, want remaining 6", book.Bids)
	}
	if len(book.Asks["1000"]) != 1 || book.Asks["1000"][0].Qty != 2 {
		t.Fatalf("asks = %+v, want remaining 2", book.Asks)
	}

	savedResting, err := f.orders.FindByID(resting.ID)
	if err != nil {
		t.Fatalf("FindByID resting: %v", err)
	}
	if savedResting.Qty != 2 || savedResting.Status != domain.OrderStatusPartiallyMatched {
		t.Fatalf("resting = %+v, want qty 2 PARTIALLY_MATCHED", savedResting)
	}

	trades, _, err := f.trades.List("", "", ulid.ULID{}, 10)
	if err != nil {
		t.Fatalf("list trades: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("stored trades = %d, want 1", len(trades))
	}

	if len(f.publisher.events) != 3 {
		t.Fatalf("events = %d, want 3", len(f.publisher.events))
	}
	if _, ok := f.publisher.events[0].(port.OrderPlacedEvent); !ok {
		t.Fatalf("event[0] = %T, want OrderPlacedEvent", f.publisher.events[0])
	}
	updated, ok := f.publisher.events[1].(port.OrderUpdatedEvent)
	if !ok {
		t.Fatalf("event[1] = %T, want OrderUpdatedEvent", f.publisher.events[1])
	}
	if updated.Order.ID != resting.ID || updated.Order.Status != domain.OrderStatusPartiallyMatched {
		t.Fatalf("updated order = %+v, want resting order PARTIALLY_MATCHED", updated.Order)
	}
	if _, ok := f.publisher.events[2].(port.TradeExecutedEvent); !ok {
		t.Fatalf("event[2] = %T, want TradeExecutedEvent", f.publisher.events[2])
	}
}

func TestPlaceOrderFullMatch(t *testing.T) {
	f := newPlaceOrderFixture(t)
	stock := seedStock(t, f.stocks, "BMRI")
	now := time.Now().UTC()

	resting := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusNew, "1000", 10, now)
	if err := f.orders.Save(&resting); err != nil {
		t.Fatalf("save resting order: %v", err)
	}
	if err := f.books.Add(&resting); err != nil {
		t.Fatalf("add resting order to book: %v", err)
	}

	f.engine.match = func(order domain.Order) (port.MatchResult, error) {
		trade := makeTrade(stock, order, resting, "1000", 10, time.Now().UTC())
		affected := resting
		affected.Qty = 0
		affected.Status = domain.OrderStatusMatch

		return port.MatchResult{
			FilledQty:      10,
			Trades:         []domain.Trade{trade},
			AffectedOrders: []domain.Order{affected},
		}, nil
	}

	result, err := f.uc.Execute(PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Order.Status != domain.OrderStatusMatch {
		t.Fatalf("status = %s, want MATCHED", result.Order.Status)
	}

	bids, err := f.books.BestBids(stock.ID, 10)
	if err != nil {
		t.Fatalf("BestBids: %v", err)
	}
	asks, err := f.books.BestAsks(stock.ID, 10)
	if err != nil {
		t.Fatalf("BestAsks: %v", err)
	}
	if len(bids) != 0 || len(asks) != 0 {
		t.Fatalf("book not empty: bids=%d asks=%d", len(bids), len(asks))
	}
}

func TestPlaceOrderConcurrentNoDoubleFill(t *testing.T) {
	stocks := memdb.NewStockRepo()
	orders := memdb.NewOrderRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	engine := matching_engine.New(books)
	t.Cleanup(engine.Close)

	uc := NewPlaceOrder(stocks, orders, trades, engine, noopPublisher{})
	stock := seedStock(t, stocks, "BMRI")

	resting := makeOrder(stock, "seller", domain.OrderSideSell, domain.OrderStatusNew, "1000", 10, time.Now().UTC())
	if err := orders.Save(&resting); err != nil {
		t.Fatalf("save resting order: %v", err)
	}
	if err := books.Add(&resting); err != nil {
		t.Fatalf("add resting order to book: %v", err)
	}

	buyers := []string{"buyer-a", "buyer-b"}
	results := make(chan PlaceOrderResult, len(buyers))
	errs := make(chan error, len(buyers))

	var wg sync.WaitGroup
	for _, buyer := range buyers {
		wg.Add(1)
		go func(userID string) {
			defer wg.Done()

			result, err := uc.Execute(PlaceOrderCommand{
				UserID:    userID,
				StockCode: "BMRI",
				Side:      domain.OrderSideBuy,
				Price:     decimal.RequireFromString("1000"),
				Qty:       10,
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(buyer)
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("Execute: %v", err)
	}

	traded := 0
	for result := range results {
		for _, trade := range result.Trades {
			traded += trade.Quantity
		}
	}
	if traded != 10 {
		t.Fatalf("traded quantity = %d, want 10 (no double fill)", traded)
	}

	stored, _, err := trades.List("", "", ulid.ULID{}, 10)
	if err != nil {
		t.Fatalf("list trades: %v", err)
	}
	storedQty := 0
	for _, trade := range stored {
		storedQty += trade.Quantity
	}
	if storedQty != 10 {
		t.Fatalf("stored trade quantity = %d, want 10", storedQty)
	}

	book, err := books.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	if len(book.Asks) != 0 {
		t.Fatalf("asks = %+v, want empty", book.Asks)
	}
	bids := book.Bids["1000"]
	if len(bids) != 1 || bids[0].Qty != 10 {
		t.Fatalf("bids = %+v, want one resting bid qty 10", bids)
	}
}

func TestPlaceOrderValidation(t *testing.T) {
	f := newPlaceOrderFixture(t)
	seedStock(t, f.stocks, "BMRI")

	valid := PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	}

	tests := []struct {
		name   string
		mutate func(*PlaceOrderCommand)
	}{
		{name: "empty user", mutate: func(c *PlaceOrderCommand) { c.UserID = "" }},
		{name: "empty stock", mutate: func(c *PlaceOrderCommand) { c.StockCode = "" }},
		{name: "invalid side", mutate: func(c *PlaceOrderCommand) { c.Side = "HOLD" }},
		{name: "zero price", mutate: func(c *PlaceOrderCommand) { c.Price = decimal.Zero }},
		{name: "negative price", mutate: func(c *PlaceOrderCommand) { c.Price = decimal.RequireFromString("-1") }},
		{name: "zero qty", mutate: func(c *PlaceOrderCommand) { c.Qty = 0 }},
		{name: "negative qty", mutate: func(c *PlaceOrderCommand) { c.Qty = -1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := valid
			tt.mutate(&cmd)

			if _, err := f.uc.Execute(cmd); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}

	if f.engine.calls != 0 {
		t.Fatalf("engine calls = %d, want 0", f.engine.calls)
	}
}

func TestPlaceOrderStockNotFound(t *testing.T) {
	f := newPlaceOrderFixture(t)

	_, err := f.uc.Execute(PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	})
	if !errors.Is(err, ErrStockNotFound) {
		t.Fatalf("err = %v, want ErrStockNotFound", err)
	}
	if f.engine.calls != 0 {
		t.Fatalf("engine calls = %d, want 0", f.engine.calls)
	}
}

func TestPlaceOrderEngineError(t *testing.T) {
	f := newPlaceOrderFixture(t)
	seedStock(t, f.stocks, "BMRI")
	f.engine.match = func(order domain.Order) (port.MatchResult, error) {
		return port.MatchResult{}, errors.New("engine down")
	}

	_, err := f.uc.Execute(PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	})
	if !errors.Is(err, ErrMatchingFailed) {
		t.Fatalf("err = %v, want ErrMatchingFailed", err)
	}

	orders, _, err := f.orders.List(port.OrderFilter{}, ulid.ULID{}, 10)
	if err != nil {
		t.Fatalf("list orders: %v", err)
	}
	if len(orders) != 0 {
		t.Fatalf("persisted orders = %d, want 0", len(orders))
	}
}

func TestPlaceOrderInvalidFilledQty(t *testing.T) {
	f := newPlaceOrderFixture(t)
	seedStock(t, f.stocks, "BMRI")
	f.engine.match = func(order domain.Order) (port.MatchResult, error) {
		return port.MatchResult{FilledQty: 11}, nil
	}

	_, err := f.uc.Execute(PlaceOrderCommand{
		UserID:    "user-1",
		StockCode: "BMRI",
		Side:      domain.OrderSideBuy,
		Price:     decimal.RequireFromString("1000"),
		Qty:       10,
	})
	if !errors.Is(err, ErrMatchingFailed) {
		t.Fatalf("err = %v, want ErrMatchingFailed", err)
	}
}
