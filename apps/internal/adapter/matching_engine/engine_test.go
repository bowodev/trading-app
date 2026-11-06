package matching_engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type engineFixture struct {
	engine *Engine
	books  port.IOrderBookRepository
	stock  domain.Stock
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()

	books := memdb.NewOrderBookRepo()
	engine := New(books)
	t.Cleanup(engine.Close)

	return &engineFixture{
		engine: engine,
		books:  books,
		stock:  domain.Stock{ID: ulid.Make(), Code: "BMRI"},
	}
}

func (f *engineFixture) seed(
	t *testing.T,
	userID string,
	side domain.OrderSideType,
	price string,
	qty int,
	createdAt time.Time,
) domain.Order {
	t.Helper()

	order := domain.Order{
		ID:        ulid.Make(),
		UserID:    userID,
		Stock:     f.stock,
		Side:      side,
		Status:    domain.OrderStatusNew,
		Price:     decimal.RequireFromString(price),
		Qty:       qty,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	if err := f.books.Add(&order); err != nil {
		t.Fatalf("seed order: %v", err)
	}

	return order
}

func (f *engineFixture) incoming(userID string, side domain.OrderSideType, price string, qty int) domain.Order {
	now := time.Now().UTC()

	return domain.Order{
		ID:        ulid.Make(),
		UserID:    userID,
		Stock:     f.stock,
		Side:      side,
		Status:    domain.OrderStatusNew,
		Price:     decimal.RequireFromString(price),
		Qty:       qty,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type handlerResult struct {
	order  domain.Order
	result port.MatchResult
	err    error
}

func collectHandler(results chan<- handlerResult) MatchHandler {
	return func(order domain.Order, result port.MatchResult, err error) {
		results <- handlerResult{order: order, result: result, err: err}
	}
}

func waitForHandler(t *testing.T, results <-chan handlerResult) handlerResult {
	t.Helper()

	select {
	case res := <-results:
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for match handler")
		return handlerResult{}
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func priceString(price domain.Price) string {
	return decimal.Decimal(price).String()
}

func TestMatchEmptyBook(t *testing.T) {
	f := newEngineFixture(t)

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "1000", 10))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 0 {
		t.Fatalf("filled qty = %d, want 0", result.FilledQty)
	}
	if len(result.Trades) != 0 || len(result.AffectedOrders) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMatchBuyNoCross(t *testing.T) {
	f := newEngineFixture(t)
	f.seed(t, "user-2", domain.OrderSideSell, "1001", 10, time.Now().UTC())

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "1000", 10))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 0 || len(result.Trades) != 0 || len(result.AffectedOrders) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMatchBuyFullFill(t *testing.T) {
	f := newEngineFixture(t)
	resting := f.seed(t, "user-2", domain.OrderSideSell, "990", 10, time.Now().UTC())
	incoming := f.incoming("user-1", domain.OrderSideBuy, "1000", 10)

	result, err := f.engine.Match(incoming)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 10 {
		t.Fatalf("filled qty = %d, want 10", result.FilledQty)
	}
	if len(result.Trades) != 1 {
		t.Fatalf("trades = %d, want 1", len(result.Trades))
	}

	trade := result.Trades[0]
	if priceString(trade.Price) != "990" {
		t.Fatalf("trade price = %s, want resting price 990", priceString(trade.Price))
	}
	if trade.Quantity != 10 {
		t.Fatalf("trade qty = %d, want 10", trade.Quantity)
	}
	if trade.BuyOrder.ID != incoming.ID || trade.SellOrder.ID != resting.ID {
		t.Fatalf("trade orders = %s/%s, want %s/%s",
			trade.BuyOrder.ID, trade.SellOrder.ID, incoming.ID, resting.ID)
	}
	if trade.SellOrder.Qty != 10 {
		t.Fatalf("trade sell snapshot qty = %d, want original 10", trade.SellOrder.Qty)
	}

	if len(result.AffectedOrders) != 1 {
		t.Fatalf("affected orders = %d, want 1", len(result.AffectedOrders))
	}
	affected := result.AffectedOrders[0]
	if affected.ID != resting.ID || affected.Qty != 0 || affected.Status != domain.OrderStatusMatch {
		t.Fatalf("affected = %+v, want resting MATCHED qty 0", affected)
	}
}

func TestMatchConsumesRestingOrder(t *testing.T) {
	f := newEngineFixture(t)
	resting := f.seed(t, "user-2", domain.OrderSideSell, "1000", 4, time.Now().UTC())
	incoming := f.incoming("user-1", domain.OrderSideBuy, "1000", 10)

	result, err := f.engine.Match(incoming)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 4 {
		t.Fatalf("filled qty = %d, want 4", result.FilledQty)
	}
	if len(result.AffectedOrders) != 1 {
		t.Fatalf("affected orders = %d, want 1", len(result.AffectedOrders))
	}
	if result.AffectedOrders[0].ID != resting.ID || result.AffectedOrders[0].Qty != 0 {
		t.Fatalf("affected = %+v, want resting qty 0", result.AffectedOrders[0])
	}
}

func TestMatchPartialRestingOrder(t *testing.T) {
	f := newEngineFixture(t)
	resting := f.seed(t, "user-2", domain.OrderSideSell, "1000", 20, time.Now().UTC())
	incoming := f.incoming("user-1", domain.OrderSideBuy, "1000", 10)

	result, err := f.engine.Match(incoming)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 10 {
		t.Fatalf("filled qty = %d, want 10", result.FilledQty)
	}
	if len(result.AffectedOrders) != 1 {
		t.Fatalf("affected orders = %d, want 1", len(result.AffectedOrders))
	}

	affected := result.AffectedOrders[0]
	if affected.ID != resting.ID || affected.Qty != 10 {
		t.Fatalf("affected = %+v, want resting qty 10", affected)
	}
	if affected.Status != domain.OrderStatusPartiallyMatched {
		t.Fatalf("status = %s, want PARTIALLY_MATCHED", affected.Status)
	}
}

func TestMatchBuyPricePriority(t *testing.T) {
	f := newEngineFixture(t)
	base := time.Now().UTC()
	expensive := f.seed(t, "user-2", domain.OrderSideSell, "100", 5, base)
	cheap := f.seed(t, "user-3", domain.OrderSideSell, "99", 5, base.Add(time.Second))

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "100", 8))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 8 {
		t.Fatalf("filled qty = %d, want 8", result.FilledQty)
	}
	if len(result.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(result.Trades))
	}
	if priceString(result.Trades[0].Price) != "99" || result.Trades[0].Quantity != 5 {
		t.Fatalf("trade[0] = %s x %d, want 99 x 5", priceString(result.Trades[0].Price), result.Trades[0].Quantity)
	}
	if priceString(result.Trades[1].Price) != "100" || result.Trades[1].Quantity != 3 {
		t.Fatalf("trade[1] = %s x %d, want 100 x 3", priceString(result.Trades[1].Price), result.Trades[1].Quantity)
	}

	if len(result.AffectedOrders) != 2 {
		t.Fatalf("affected orders = %d, want 2", len(result.AffectedOrders))
	}
	if result.AffectedOrders[0].ID != cheap.ID || result.AffectedOrders[0].Qty != 0 {
		t.Fatalf("affected[0] = %+v, want cheap order fully filled", result.AffectedOrders[0])
	}
	if result.AffectedOrders[1].ID != expensive.ID || result.AffectedOrders[1].Qty != 2 {
		t.Fatalf("affected[1] = %+v, want expensive order qty 2", result.AffectedOrders[1])
	}
}

func TestMatchBuyTimePriority(t *testing.T) {
	f := newEngineFixture(t)
	base := time.Now().UTC()
	newer := f.seed(t, "user-2", domain.OrderSideSell, "100", 3, base.Add(time.Second))
	older := f.seed(t, "user-3", domain.OrderSideSell, "100", 3, base)

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "100", 4))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(result.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(result.Trades))
	}
	if result.Trades[0].SellOrder.ID != older.ID || result.Trades[0].Quantity != 3 {
		t.Fatalf("trade[0] = %+v, want older order filled first", result.Trades[0])
	}
	if result.Trades[1].SellOrder.ID != newer.ID || result.Trades[1].Quantity != 1 {
		t.Fatalf("trade[1] = %+v, want newer order filled second", result.Trades[1])
	}
}

func TestMatchSellPricePriority(t *testing.T) {
	f := newEngineFixture(t)
	base := time.Now().UTC()
	lower := f.seed(t, "user-2", domain.OrderSideBuy, "100", 5, base)
	higher := f.seed(t, "user-3", domain.OrderSideBuy, "101", 5, base.Add(time.Second))
	incoming := f.incoming("user-1", domain.OrderSideSell, "100", 7)

	result, err := f.engine.Match(incoming)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 7 {
		t.Fatalf("filled qty = %d, want 7", result.FilledQty)
	}
	if len(result.Trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(result.Trades))
	}
	if priceString(result.Trades[0].Price) != "101" || result.Trades[0].Quantity != 5 {
		t.Fatalf("trade[0] = %s x %d, want 101 x 5", priceString(result.Trades[0].Price), result.Trades[0].Quantity)
	}
	if result.Trades[0].BuyOrder.ID != higher.ID || result.Trades[0].SellOrder.ID != incoming.ID {
		t.Fatalf("trade[0] orders = %+v, want higher bid vs incoming sell", result.Trades[0])
	}
	if priceString(result.Trades[1].Price) != "100" || result.Trades[1].Quantity != 2 {
		t.Fatalf("trade[1] = %s x %d, want 100 x 2", priceString(result.Trades[1].Price), result.Trades[1].Quantity)
	}
	if result.Trades[1].BuyOrder.ID != lower.ID {
		t.Fatalf("trade[1] buy order = %s, want %s", result.Trades[1].BuyOrder.ID, lower.ID)
	}
}

func TestMatchSkipsSelfTrade(t *testing.T) {
	f := newEngineFixture(t)
	base := time.Now().UTC()
	own := f.seed(t, "user-1", domain.OrderSideSell, "99", 5, base)
	other := f.seed(t, "user-2", domain.OrderSideSell, "100", 5, base.Add(time.Second))

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "100", 5))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 5 {
		t.Fatalf("filled qty = %d, want 5", result.FilledQty)
	}
	if len(result.Trades) != 1 {
		t.Fatalf("trades = %d, want 1", len(result.Trades))
	}
	if result.Trades[0].SellOrder.ID != other.ID {
		t.Fatalf("matched sell order = %s, want %s", result.Trades[0].SellOrder.ID, other.ID)
	}
	if priceString(result.Trades[0].Price) != "100" {
		t.Fatalf("trade price = %s, want 100", priceString(result.Trades[0].Price))
	}
	for _, affected := range result.AffectedOrders {
		if affected.ID == own.ID {
			t.Fatalf("self order must not be affected: %+v", affected)
		}
	}
}

func TestMatchSelfTradeOnly(t *testing.T) {
	f := newEngineFixture(t)
	f.seed(t, "user-1", domain.OrderSideSell, "100", 5, time.Now().UTC())

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "100", 5))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 0 || len(result.Trades) != 0 || len(result.AffectedOrders) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMatchInvalidOrder(t *testing.T) {
	f := newEngineFixture(t)

	tests := []struct {
		name  string
		order domain.Order
	}{
		{name: "invalid side", order: f.incoming("user-1", domain.OrderSideType("HOLD"), "1000", 10)},
		{name: "zero qty", order: f.incoming("user-1", domain.OrderSideBuy, "1000", 0)},
		{name: "negative qty", order: f.incoming("user-1", domain.OrderSideBuy, "1000", -1)},
		{name: "zero price", order: f.incoming("user-1", domain.OrderSideBuy, "0", 10)},
		{name: "negative price", order: f.incoming("user-1", domain.OrderSideBuy, "-1", 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.engine.Match(tt.order); !errors.Is(err, port.ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

type failingBookRepo struct {
	port.IOrderBookRepository
	err error
}

func (r failingBookRepo) Get(ulid.ULID) (*domain.OrderBook, error) {
	return nil, r.err
}

func TestMatchPropagatesRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	engine := New(failingBookRepo{err: boom})
	t.Cleanup(engine.Close)

	order := domain.Order{
		ID:     ulid.Make(),
		UserID: "user-1",
		Stock:  domain.Stock{ID: ulid.Make(), Code: "BMRI"},
		Side:   domain.OrderSideBuy,
		Price:  decimal.RequireFromString("1000"),
		Qty:    10,
	}

	if _, err := engine.Match(order); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}

func TestSubmitFullMatchUpdatesBook(t *testing.T) {
	f := newEngineFixture(t)
	resting := f.seed(t, "user-2", domain.OrderSideSell, "1000", 10, time.Now().UTC())
	results := make(chan handlerResult, 1)
	incoming := f.incoming("user-1", domain.OrderSideBuy, "1000", 10)

	if err := f.engine.Submit(incoming, collectHandler(results)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	res := waitForHandler(t, results)
	if res.err != nil {
		t.Fatalf("handler err = %v, want nil", res.err)
	}
	if res.result.FilledQty != 10 {
		t.Fatalf("filled qty = %d, want 10", res.result.FilledQty)
	}
	if len(res.result.Trades) != 1 || res.result.Trades[0].SellOrder.ID != resting.ID {
		t.Fatalf("trades = %+v, want resting order filled", res.result.Trades)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	if len(book.Asks) != 0 || len(book.Bids) != 0 {
		t.Fatalf("book = %+v, want empty", book)
	}
}

func TestSubmitNoMatchRestsNew(t *testing.T) {
	f := newEngineFixture(t)
	results := make(chan handlerResult, 1)

	if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 10), collectHandler(results)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	res := waitForHandler(t, results)
	if res.err != nil {
		t.Fatalf("handler err = %v, want nil", res.err)
	}
	if res.result.FilledQty != 0 || len(res.result.Trades) != 0 {
		t.Fatalf("unexpected result: %+v", res.result)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	bids := book.Bids["1000"]
	if len(bids) != 1 || bids[0].Qty != 10 || bids[0].Status != domain.OrderStatusNew {
		t.Fatalf("bids = %+v, want NEW qty 10", bids)
	}
}

func TestSubmitPartialIncomingRests(t *testing.T) {
	f := newEngineFixture(t)
	f.seed(t, "user-2", domain.OrderSideSell, "1000", 4, time.Now().UTC())
	results := make(chan handlerResult, 1)

	if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 10), collectHandler(results)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	res := waitForHandler(t, results)
	if res.err != nil {
		t.Fatalf("handler err = %v, want nil", res.err)
	}
	if res.result.FilledQty != 4 {
		t.Fatalf("filled qty = %d, want 4", res.result.FilledQty)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	if len(book.Asks) != 0 {
		t.Fatalf("asks = %+v, want empty", book.Asks)
	}
	bids := book.Bids["1000"]
	if len(bids) != 1 || bids[0].Qty != 6 || bids[0].Status != domain.OrderStatusPartiallyMatched {
		t.Fatalf("bids = %+v, want PARTIALLY_MATCHED qty 6", bids)
	}
}

func TestSubmitPartialRestingOrderUpdatesBook(t *testing.T) {
	f := newEngineFixture(t)
	f.seed(t, "user-2", domain.OrderSideSell, "1000", 20, time.Now().UTC())
	results := make(chan handlerResult, 1)

	if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 10), collectHandler(results)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	res := waitForHandler(t, results)
	if res.err != nil {
		t.Fatalf("handler err = %v, want nil", res.err)
	}
	if res.result.FilledQty != 10 {
		t.Fatalf("filled qty = %d, want 10", res.result.FilledQty)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	if len(book.Bids) != 0 {
		t.Fatalf("bids = %+v, want empty", book.Bids)
	}
	asks := book.Asks["1000"]
	if len(asks) != 1 || asks[0].Qty != 10 || asks[0].Status != domain.OrderStatusPartiallyMatched {
		t.Fatalf("asks = %+v, want PARTIALLY_MATCHED qty 10", asks)
	}
}

func TestSubmitSelfTradeSkippedUpdatesBook(t *testing.T) {
	f := newEngineFixture(t)
	base := time.Now().UTC()
	f.seed(t, "user-1", domain.OrderSideSell, "99", 5, base)
	other := f.seed(t, "user-2", domain.OrderSideSell, "100", 5, base.Add(time.Second))
	results := make(chan handlerResult, 1)

	if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "100", 5), collectHandler(results)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	res := waitForHandler(t, results)
	if res.err != nil {
		t.Fatalf("handler err = %v, want nil", res.err)
	}
	if res.result.FilledQty != 5 || len(res.result.Trades) != 1 {
		t.Fatalf("unexpected result: %+v", res.result)
	}
	if res.result.Trades[0].SellOrder.ID != other.ID {
		t.Fatalf("matched sell order = %s, want %s", res.result.Trades[0].SellOrder.ID, other.ID)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	ownAsks := book.Asks["99"]
	if len(ownAsks) != 1 || ownAsks[0].Qty != 5 {
		t.Fatalf("own asks = %+v, want qty 5 untouched", ownAsks)
	}
	if len(book.Asks["100"]) != 0 {
		t.Fatalf("other ask = %+v, want removed", book.Asks["100"])
	}
}

func TestMatchCommitsBook(t *testing.T) {
	f := newEngineFixture(t)
	f.seed(t, "user-2", domain.OrderSideSell, "1000", 10, time.Now().UTC())

	result, err := f.engine.Match(f.incoming("user-1", domain.OrderSideBuy, "1000", 4))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.FilledQty != 4 {
		t.Fatalf("filled qty = %d, want 4", result.FilledQty)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	asks := book.Asks["1000"]
	if len(asks) != 1 || asks[0].Qty != 6 {
		t.Fatalf("asks = %+v, want remaining qty 6", asks)
	}
	if len(book.Bids) != 0 {
		t.Fatalf("bids = %+v, want empty", book.Bids)
	}
}

func TestConcurrentMatchDoesNotDoubleFill(t *testing.T) {
	f := newEngineFixture(t)
	f.seed(t, "user-2", domain.OrderSideSell, "1000", 10, time.Now().UTC())

	const buyers = 2
	filled := make(chan int, buyers)
	var wg sync.WaitGroup
	wg.Add(buyers)

	for i := 0; i < buyers; i++ {
		go func(i int) {
			defer wg.Done()

			result, err := f.engine.Match(f.incoming(
				fmt.Sprintf("buyer-%d", i),
				domain.OrderSideBuy,
				"1000",
				10,
			))
			if err != nil {
				t.Errorf("Match: %v", err)
				return
			}
			filled <- result.FilledQty
		}(i)
	}
	wg.Wait()
	close(filled)

	total := 0
	for qty := range filled {
		total += qty
	}
	if total != 10 {
		t.Fatalf("total filled qty = %d, want 10", total)
	}

	book, err := f.books.Get(f.stock.ID)
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

func TestConcurrentSubmitStress(t *testing.T) {
	f := newEngineFixture(t)
	const perSide = 50
	total := perSide * 2
	results := make(chan handlerResult, total)

	stopReaders := make(chan struct{})
	readerDone := make(chan struct{})
	readerErrs := make(chan error, 1)
	go func() {
		defer close(readerDone)

		for {
			select {
			case <-stopReaders:
				return
			default:
			}

			bids, asks, err := f.books.Snapshot(f.stock.ID, 10)
			if err != nil {
				if errors.Is(err, port.ErrNotFound) {
					continue
				}
				readerErrs <- err
				return
			}

			for _, levels := range [][]domain.Order{bids, asks} {
				for _, order := range levels {
					if order.Qty <= 0 {
						readerErrs <- fmt.Errorf("snapshot returned non-positive qty %d", order.Qty)
						return
					}
				}
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(total)

	for i := 0; i < total; i++ {
		side := domain.OrderSideBuy
		user := "buyer"
		if i%2 == 1 {
			side = domain.OrderSideSell
			user = "seller"
		}

		go func(side domain.OrderSideType, user string) {
			defer wg.Done()

			if err := f.engine.Submit(f.incoming(user, side, "1000", 1), collectHandler(results)); err != nil {
				t.Errorf("Submit: %v", err)
			}
		}(side, user)
	}
	wg.Wait()

	trades := 0
	quantity := 0
	for i := 0; i < total; i++ {
		res := waitForHandler(t, results)
		if res.err != nil {
			t.Fatalf("handler err = %v, want nil", res.err)
		}
		trades += len(res.result.Trades)
		for _, trade := range res.result.Trades {
			quantity += trade.Quantity
		}
	}

	if trades != perSide {
		t.Fatalf("trades = %d, want %d", trades, perSide)
	}
	if quantity != perSide {
		t.Fatalf("traded quantity = %d, want %d", quantity, perSide)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	if len(book.Bids) != 0 || len(book.Asks) != 0 {
		t.Fatalf("book = %+v, want empty", book)
	}

	close(stopReaders)
	<-readerDone
	select {
	case err := <-readerErrs:
		t.Fatalf("snapshot reader: %v", err)
	default:
	}
}

func TestSubmitOrdering(t *testing.T) {
	f := newEngineFixture(t)
	const count = 10
	results := make(chan handlerResult, count)
	orders := make([]domain.Order, 0, count)

	for i := 0; i < count; i++ {
		order := f.incoming("user-1", domain.OrderSideBuy, "1000", 1)
		orders = append(orders, order)
		if err := f.engine.Submit(order, collectHandler(results)); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	for i := 0; i < count; i++ {
		res := waitForHandler(t, results)
		if res.err != nil {
			t.Fatalf("handler %d err = %v, want nil", i, res.err)
		}
		if res.order.ID != orders[i].ID {
			t.Fatalf("handler %d order = %s, want %s", i, res.order.ID, orders[i].ID)
		}
	}
}

func TestSubmitBackpressure(t *testing.T) {
	f := newEngineFixture(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	t.Cleanup(unblock)

	started := make(chan struct{})
	var startedOnce sync.Once
	handler := func(domain.Order, port.MatchResult, error) {
		startedOnce.Do(func() {
			close(started)
		})
		<-release
	}

	if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 1), handler); err != nil {
		t.Fatalf("Submit first: %v", err)
	}
	waitForSignal(t, started)

	for i := 0; i < defaultQueueSize; i++ {
		if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 1), handler); err != nil {
			t.Fatalf("Submit filler %d: %v", i, err)
		}
	}

	blocked := make(chan error, 1)
	go func() {
		blocked <- f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 1), handler)
	}()

	select {
	case err := <-blocked:
		t.Fatalf("Submit returned before a queue slot opened: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	unblock()

	select {
	case err := <-blocked:
		if err != nil {
			t.Fatalf("Submit after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not unblock after queue drained")
	}
}

func TestSubmitAfterCloseReturnsError(t *testing.T) {
	f := newEngineFixture(t)
	f.engine.Close()

	order := f.incoming("user-1", domain.OrderSideBuy, "1000", 10)
	if err := f.engine.Submit(order, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit err = %v, want ErrClosed", err)
	}
	if _, err := f.engine.Match(order); !errors.Is(err, ErrClosed) {
		t.Fatalf("Match err = %v, want ErrClosed", err)
	}
}

func TestCloseDrainsQueuedRequests(t *testing.T) {
	f := newEngineFixture(t)
	const count = 10
	results := make(chan handlerResult, count)

	for i := 0; i < count; i++ {
		if err := f.engine.Submit(f.incoming("user-1", domain.OrderSideBuy, "1000", 1), collectHandler(results)); err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}

	f.engine.Close()

	for i := 0; i < count; i++ {
		res := waitForHandler(t, results)
		if res.err != nil {
			t.Fatalf("handler %d err = %v, want nil", i, res.err)
		}
	}
}

type commitFailRepo struct {
	port.IOrderBookRepository
	err error
}

func (r commitFailRepo) Get(ulid.ULID) (*domain.OrderBook, error) {
	return nil, port.ErrNotFound
}

func (r commitFailRepo) Add(*domain.Order) error {
	return r.err
}

func (r commitFailRepo) Remove(ulid.ULID, ulid.ULID) error {
	return r.err
}

func (r commitFailRepo) Update(*domain.Order) error {
	return r.err
}

func (r commitFailRepo) Apply(domain.Stock, port.OrderBookChange) error {
	return r.err
}

func TestSubmitHandlerReceivesCommitError(t *testing.T) {
	boom := errors.New("commit boom")
	engine := New(commitFailRepo{err: boom})
	t.Cleanup(engine.Close)

	results := make(chan handlerResult, 1)
	order := domain.Order{
		ID:     ulid.Make(),
		UserID: "user-1",
		Stock:  domain.Stock{ID: ulid.Make(), Code: "BMRI"},
		Side:   domain.OrderSideBuy,
		Price:  decimal.RequireFromString("1000"),
		Qty:    10,
	}

	if err := engine.Submit(order, collectHandler(results)); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	res := waitForHandler(t, results)
	if !errors.Is(res.err, boom) {
		t.Fatalf("handler err = %v, want %v", res.err, boom)
	}
}

func TestSubmitInvalidOrder(t *testing.T) {
	f := newEngineFixture(t)

	tests := []struct {
		name  string
		order domain.Order
	}{
		{name: "invalid side", order: f.incoming("user-1", domain.OrderSideType("HOLD"), "1000", 10)},
		{name: "zero qty", order: f.incoming("user-1", domain.OrderSideBuy, "1000", 0)},
		{name: "negative qty", order: f.incoming("user-1", domain.OrderSideBuy, "1000", -1)},
		{name: "zero price", order: f.incoming("user-1", domain.OrderSideBuy, "0", 10)},
		{name: "negative price", order: f.incoming("user-1", domain.OrderSideBuy, "-1", 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := f.engine.Submit(tt.order, nil); !errors.Is(err, port.ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
