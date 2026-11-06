package memdb

import (
	"sort"
	"sync"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type orderBookRepo struct {
	mu        *sync.RWMutex
	byStockId map[ulid.ULID]domain.OrderBook
}

type priceLevel struct {
	price decimal.Decimal
	key   string
}

func NewOrderBookRepo() port.IOrderBookRepository {
	return &orderBookRepo{
		mu:        &sync.RWMutex{},
		byStockId: make(map[ulid.ULID]domain.OrderBook),
	}
}

func newOrderBook(stock domain.Stock) domain.OrderBook {
	return domain.OrderBook{
		Stock: stock,
		Asks:  make(map[string][]domain.Order),
		Bids:  make(map[string][]domain.Order),
	}
}

func cloneOrderBook(book domain.OrderBook) domain.OrderBook {
	return domain.OrderBook{
		Stock: book.Stock,
		Asks:  clonePriceLevels(book.Asks),
		Bids:  clonePriceLevels(book.Bids),
	}
}

func clonePriceLevels(levels map[string][]domain.Order) map[string][]domain.Order {
	cloned := make(map[string][]domain.Order, len(levels))
	for price, orders := range levels {
		cloned[price] = append([]domain.Order(nil), orders...)
	}
	return cloned
}

func (r *orderBookRepo) Get(stockId ulid.ULID) (*domain.OrderBook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	book, found := r.byStockId[stockId]
	if !found {
		return nil, ErrDataNotFound
	}

	cloned := cloneOrderBook(book)
	return &cloned, nil
}

func (r *orderBookRepo) Add(order *domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.addLocked(order)
}

func (r *orderBookRepo) addLocked(order *domain.Order) error {
	if order == nil || !isValidSide(order.Side) {
		return ErrInvalidData
	}

	book, found := r.byStockId[order.Stock.ID]
	if !found {
		book = newOrderBook(order.Stock)
	}

	if err := addOrderToBook(book, order); err != nil {
		return err
	}

	r.byStockId[order.Stock.ID] = book
	return nil
}

func addOrderToBook(book domain.OrderBook, order *domain.Order) error {
	if order == nil || !isValidSide(order.Side) {
		return ErrInvalidData
	}

	priceKey := order.Price.String()
	if order.Side == domain.OrderSideBuy {
		book.Bids[priceKey] = append(book.Bids[priceKey], *order)
	} else {
		book.Asks[priceKey] = append(book.Asks[priceKey], *order)
	}

	return nil
}

// Apply mutates a clone of the book and stores it only when every change
// succeeds, so readers never observe a partially applied match.
func (r *orderBookRepo) Apply(stock domain.Stock, change port.OrderBookChange) error {
	if stock.ID == (ulid.ULID{}) {
		return ErrInvalidData
	}
	if len(change.Removes) == 0 && len(change.Upserts) == 0 {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	book, found := r.byStockId[stock.ID]
	if !found {
		book = newOrderBook(stock)
	} else {
		book = cloneOrderBook(book)
	}

	for _, orderId := range change.Removes {
		if !removeOrderLocked(book, orderId) {
			return ErrDataNotFound
		}
	}

	for i := range change.Upserts {
		order := change.Upserts[i]
		if order.Stock.ID != stock.ID || order.ID == (ulid.ULID{}) || !isValidSide(order.Side) {
			return ErrInvalidData
		}

		removeOrderLocked(book, order.ID)
		if err := addOrderToBook(book, &order); err != nil {
			return err
		}
	}

	r.byStockId[stock.ID] = book
	return nil
}

func isValidSide(side domain.OrderSideType) bool {
	return side == domain.OrderSideBuy || side == domain.OrderSideSell
}

func (r *orderBookRepo) Remove(stockId, orderId ulid.ULID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	book, found := r.byStockId[stockId]
	if !found {
		return ErrDataNotFound
	}

	if !removeOrderLocked(book, orderId) {
		return ErrDataNotFound
	}

	r.byStockId[stockId] = book
	return nil
}

func removeOrderLocked(book domain.OrderBook, orderId ulid.ULID) bool {
	if removeOrderFromLevels(book.Bids, orderId) {
		return true
	}
	return removeOrderFromLevels(book.Asks, orderId)
}

func removeOrderFromLevels(levels map[string][]domain.Order, orderId ulid.ULID) bool {
	for price, orders := range levels {
		for i, order := range orders {
			if order.ID != orderId {
				continue
			}

			if len(orders) == 1 {
				delete(levels, price)
			} else {
				levels[price] = append(orders[:i], orders[i+1:]...)
			}
			return true
		}
	}

	return false
}

// Update replaces an existing order and re-inserts it into the correct price
// level, so price or side changes are reflected in the book.
func (r *orderBookRepo) Update(order *domain.Order) error {
	if order == nil || !isValidSide(order.Side) {
		return ErrInvalidData
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	book, found := r.byStockId[order.Stock.ID]
	if !found {
		return ErrDataNotFound
	}

	if !removeOrderLocked(book, order.ID) {
		return ErrDataNotFound
	}

	if err := addOrderToBook(book, order); err != nil {
		return err
	}

	r.byStockId[order.Stock.ID] = book
	return nil
}

func (r *orderBookRepo) BestBids(stockId ulid.ULID, limit int) ([]domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	book, found := r.byStockId[stockId]
	if !found {
		return nil, ErrDataNotFound
	}

	return bestOrders(book.Bids, limit, true), nil
}

func (r *orderBookRepo) BestAsks(stockId ulid.ULID, limit int) ([]domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	book, found := r.byStockId[stockId]
	if !found {
		return nil, ErrDataNotFound
	}

	return bestOrders(book.Asks, limit, false), nil
}

// Snapshot reads bids and asks under one read lock so callers cannot observe a
// half-applied match between the two sides.
func (r *orderBookRepo) Snapshot(stockId ulid.ULID, depth int) ([]domain.Order, []domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	book, found := r.byStockId[stockId]
	if !found {
		return nil, nil, ErrDataNotFound
	}

	return bestOrders(book.Bids, depth, true), bestOrders(book.Asks, depth, false), nil
}

func bestOrders(levels map[string][]domain.Order, limit int, descending bool) []domain.Order {
	if limit <= 0 {
		return []domain.Order{}
	}

	total := 0
	for _, orders := range levels {
		total += len(orders)
	}
	if total == 0 {
		return []domain.Order{}
	}
	if limit > total {
		limit = total
	}

	prices := make([]priceLevel, 0, len(levels))
	for priceKey := range levels {
		price, err := decimal.NewFromString(priceKey)
		if err != nil {
			continue
		}
		prices = append(prices, priceLevel{price: price, key: priceKey})
	}

	sort.Slice(prices, func(i, j int) bool {
		if descending {
			return prices[i].price.GreaterThan(prices[j].price)
		}
		return prices[i].price.LessThan(prices[j].price)
	})

	result := make([]domain.Order, 0, limit)
	for _, level := range prices {
		for _, order := range levels[level.key] {
			result = append(result, order)
			if len(result) == limit {
				return result
			}
		}
	}

	return result
}
