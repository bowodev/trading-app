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

// List returns orders sorted from newest to oldest. Cursor is a zero-based
// offset into the filtered result. Next is the ID of the last returned order
// when there are more rows, otherwise the zero ULID.
func (r *orderRepo) List(filterType, filterValue string, cursor, limit int) ([]domain.Order, ulid.ULID, error) {
	if cursor < 0 {
		return nil, ulid.ULID{}, ErrInvalidArgument
	}
	if err := validateOrderFilter(filterType); err != nil {
		return nil, ulid.ULID{}, err
	}
	if limit <= 0 {
		limit = defaultListLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	orders := make([]domain.Order, 0, len(r.byID))
	for _, order := range r.byID {
		if !matchOrderFilter(order, filterType, filterValue) {
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

	if cursor >= len(orders) {
		return []domain.Order{}, ulid.ULID{}, nil
	}

	end := cursor + limit
	if end > len(orders) {
		end = len(orders)
	}

	result := orders[cursor:end]
	var next ulid.ULID
	if end < len(orders) {
		next = result[len(result)-1].ID
	}

	return result, next, nil
}

func validateOrderFilter(filterType string) error {
	switch filterType {
	case "", "stockCode", "status", "userId", "userID":
		return nil
	default:
		return ErrInvalidArgument
	}
}

func matchOrderFilter(o domain.Order, filterType, filterValue string) bool {
	switch filterType {
	case "":
		return true
	case "stockCode":
		return o.Stock.Code == filterValue
	case "status":
		return string(o.Status) == filterValue
	case "userId", "userID":
		return o.UserID == filterValue
	default:
		return false
	}
}
