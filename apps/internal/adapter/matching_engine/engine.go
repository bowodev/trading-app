package matching_engine

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

const defaultQueueSize = 64

var ErrClosed = errors.New("matching engine is closed")

// MatchHandler runs in the engine goroutine and must not call Submit, Match or Close.
type MatchHandler func(order domain.Order, result port.MatchResult, err error)

type Engine struct {
	books port.IOrderBookRepository

	requests chan request
	stop     chan struct{}
	done     chan struct{}

	stateMu  sync.RWMutex
	closed   bool
	stopOnce sync.Once
}

type request struct {
	order     domain.Order
	reply     chan response
	onMatched MatchHandler
}

type response struct {
	result port.MatchResult
	err    error
}

func New(books port.IOrderBookRepository) *Engine {
	engine := &Engine{
		books:    books,
		requests: make(chan request, defaultQueueSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go engine.loop()

	return engine
}

// Submit blocks when the queue is full; the callback receives the result or error.
func (e *Engine) Submit(order domain.Order, onMatched MatchHandler) error {
	if err := validateOrder(order); err != nil {
		return err
	}

	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	if e.closed {
		return ErrClosed
	}

	e.requests <- request{
		order:     order,
		onMatched: onMatched,
	}

	return nil
}

// Match waits until the book change is applied before returning.
func (e *Engine) Match(order domain.Order) (port.MatchResult, error) {
	if err := validateOrder(order); err != nil {
		return port.MatchResult{}, err
	}

	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	if e.closed {
		return port.MatchResult{}, ErrClosed
	}

	reply := make(chan response, 1)
	e.requests <- request{
		order: order,
		reply: reply,
	}

	result := <-reply
	return result.result, result.err
}

// Close is idempotent and waits for the engine goroutine to exit.
func (e *Engine) Close() {
	e.stopOnce.Do(func() {
		e.stateMu.Lock()
		e.closed = true
		close(e.stop)
		e.stateMu.Unlock()

		<-e.done
	})
}

func (e *Engine) loop() {
	defer close(e.done)

	for {
		select {
		case req := <-e.requests:
			e.handle(req)
		case <-e.stop:
			e.drain()
			return
		}
	}
}

func (e *Engine) drain() {
	for {
		select {
		case req := <-e.requests:
			e.handle(req)
		default:
			return
		}
	}
}

func (e *Engine) handle(req request) {
	result, err := e.match(req.order)
	if err == nil {
		err = e.commit(req.order, result)
	}

	if req.reply != nil {
		req.reply <- response{result: result, err: err}
	}
	if req.onMatched != nil {
		req.onMatched(req.order, result, err)
	}
}

func (e *Engine) match(order domain.Order) (port.MatchResult, error) {
	if err := validateOrder(order); err != nil {
		return port.MatchResult{}, err
	}

	book, err := e.books.Get(order.Stock.ID)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return port.MatchResult{}, nil
		}
		return port.MatchResult{}, err
	}
	if book == nil {
		return port.MatchResult{}, nil
	}

	candidates := oppositeOrders(book, order.Side)
	sortCandidates(candidates, order.Side)

	now := time.Now().UTC()
	remaining := order.Qty
	result := port.MatchResult{}

	for i := range candidates {
		if remaining == 0 {
			break
		}

		resting := candidates[i]
		if resting.Qty <= 0 {
			continue
		}
		if !crosses(order.Side, order.Price, resting.Price) {
			break
		}
		if resting.UserID == order.UserID {
			continue
		}

		qty := remaining
		if resting.Qty < qty {
			qty = resting.Qty
		}

		buyOrder, sellOrder := tradeOrders(order, resting)
		result.Trades = append(result.Trades, domain.Trade{
			ID:        ulid.Make(),
			Stock:     order.Stock,
			Price:     domain.Price(resting.Price),
			Quantity:  qty,
			BuyOrder:  buyOrder,
			SellOrder: sellOrder,
			CreatedAt: now,
		})

		affected := resting
		affected.Qty = resting.Qty - qty
		affected.Status = restingStatus(affected.Qty)
		result.AffectedOrders = append(result.AffectedOrders, affected)

		remaining -= qty
	}

	result.FilledQty = order.Qty - remaining
	return result, nil
}

func (e *Engine) commit(order domain.Order, result port.MatchResult) error {
	now := time.Now().UTC()
	change := port.OrderBookChange{}

	for i := range result.AffectedOrders {
		affected := result.AffectedOrders[i]
		affected.UpdatedAt = now
		result.AffectedOrders[i] = affected

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

	// Apply replaces the book in one step, so readers never see a half-matched state.
	return e.books.Apply(order.Stock, change)
}

func validateOrder(order domain.Order) error {
	if !isValidSide(order.Side) || order.Qty <= 0 || !order.Price.IsPositive() {
		return port.ErrInvalidArgument
	}
	return nil
}

func oppositeOrders(book *domain.OrderBook, side domain.OrderSideType) []domain.Order {
	levels := book.Asks
	if side == domain.OrderSideSell {
		levels = book.Bids
	}

	orders := make([]domain.Order, 0)
	for _, level := range levels {
		orders = append(orders, level...)
	}

	return orders
}

func sortCandidates(orders []domain.Order, side domain.OrderSideType) {
	sort.Slice(orders, func(i, j int) bool {
		left, right := orders[i], orders[j]

		if !left.Price.Equal(right.Price) {
			if side == domain.OrderSideBuy {
				return left.Price.LessThan(right.Price)
			}
			return left.Price.GreaterThan(right.Price)
		}
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return bytes.Compare(left.ID[:], right.ID[:]) < 0
	})
}

func crosses(side domain.OrderSideType, limitPrice, restingPrice decimal.Decimal) bool {
	if side == domain.OrderSideBuy {
		return restingPrice.LessThanOrEqual(limitPrice)
	}
	return restingPrice.GreaterThanOrEqual(limitPrice)
}

func tradeOrders(incoming, resting domain.Order) (domain.Order, domain.Order) {
	if incoming.Side == domain.OrderSideBuy {
		return incoming, resting
	}
	return resting, incoming
}

func restingStatus(remainingQty int) domain.OrderStatusType {
	if remainingQty <= 0 {
		return domain.OrderStatusMatch
	}
	return domain.OrderStatusPartiallyMatched
}

func isValidSide(side domain.OrderSideType) bool {
	return side == domain.OrderSideBuy || side == domain.OrderSideSell
}
