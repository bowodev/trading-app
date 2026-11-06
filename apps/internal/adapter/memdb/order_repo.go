package memdb

import (
	"bytes"
	"sort"
	"sync"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
)

type orderRepo struct {
	mu   *sync.RWMutex
	byID map[ulid.ULID]domain.Order
}

func NewOrderRepo() port.IOrderRepository {
	return &orderRepo{
		mu:   &sync.RWMutex{},
		byID: make(map[ulid.ULID]domain.Order),
	}
}

func (r *orderRepo) Save(o *domain.Order) error {
	if o == nil || o.ID == (ulid.ULID{}) {
		return ErrInvalidData
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.byID[o.ID] = *o
	return nil
}

func (r *orderRepo) FindByID(id ulid.ULID) (*domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	order, found := r.byID[id]
	if !found {
		return nil, ErrDataNotFound
	}

	return &order, nil
}

func (r *orderRepo) Update(o *domain.Order) error {
	if o == nil || o.ID == (ulid.ULID{}) {
		return ErrInvalidData
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, found := r.byID[o.ID]; !found {
		return ErrDataNotFound
	}

	r.byID[o.ID] = *o
	return nil
}

// List returns orders sorted from newest to oldest. Cursor is the ID of the
// last order from the previous page; the zero ULID starts at the first page.
// Next is the ID of the last returned order when there are more rows,
// otherwise the zero ULID.
func (r *orderRepo) List(filter port.OrderFilter, cursor ulid.ULID, limit int) ([]domain.Order, ulid.ULID, error) {
	if filter.Status != "" && !isValidOrderStatus(filter.Status) {
		return nil, ulid.ULID{}, ErrInvalidArgument
	}
	if limit <= 0 {
		limit = defaultListLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	orders := make([]domain.Order, 0, len(r.byID))
	for _, order := range r.byID {
		if !matchOrderFilter(order, filter) {
			continue
		}
		orders = append(orders, order)
	}

	sort.Slice(orders, func(i, j int) bool {
		if orders[i].CreatedAt.Equal(orders[j].CreatedAt) {
			return bytes.Compare(orders[i].ID[:], orders[j].ID[:]) > 0
		}
		return orders[i].CreatedAt.After(orders[j].CreatedAt)
	})

	start, err := cursorStart(orders, cursor)
	if err != nil {
		return nil, ulid.ULID{}, err
	}
	if start >= len(orders) {
		return []domain.Order{}, ulid.ULID{}, nil
	}

	end := start + limit
	if end > len(orders) {
		end = len(orders)
	}

	result := orders[start:end]
	var next ulid.ULID
	if end < len(orders) {
		next = result[len(result)-1].ID
	}

	return result, next, nil
}

func cursorStart(orders []domain.Order, cursor ulid.ULID) (int, error) {
	if cursor == (ulid.ULID{}) {
		return 0, nil
	}

	for i, order := range orders {
		if order.ID == cursor {
			return i + 1, nil
		}
	}

	return 0, ErrInvalidArgument
}

func isValidOrderStatus(status domain.OrderStatusType) bool {
	switch status {
	case domain.OrderStatusNew, domain.OrderStatusPartiallyMatched, domain.OrderStatusMatch:
		return true
	default:
		return false
	}
}

func matchOrderFilter(o domain.Order, filter port.OrderFilter) bool {
	if filter.UserID != "" && o.UserID != filter.UserID {
		return false
	}
	if filter.StockCode != "" && o.Stock.Code != filter.StockCode {
		return false
	}
	if filter.Status != "" && o.Status != filter.Status {
		return false
	}
	return true
}
