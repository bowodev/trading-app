package memdb

import (
	"bytes"
	"sort"
	"sync"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
)

type tradeRepo struct {
	mu   *sync.RWMutex
	byID map[ulid.ULID]domain.Trade
}

func NewTradeRepo() port.ITradeRepository {
	return &tradeRepo{
		mu:   &sync.RWMutex{},
		byID: make(map[ulid.ULID]domain.Trade),
	}
}

func (r *tradeRepo) Save(t *domain.Trade) error {
	if t == nil || t.ID == (ulid.ULID{}) {
		return ErrInvalidData
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.byID[t.ID] = *t
	return nil
}

// List returns trades sorted from newest to oldest. Cursor is a zero-based
// offset into the filtered result. Next is the ID of the last returned trade
// when there are more rows, otherwise the zero ULID.
func (r *tradeRepo) List(filterType, filterValue string, cursor, limit int) ([]domain.Trade, ulid.ULID, error) {
	if cursor < 0 {
		return nil, ulid.ULID{}, ErrInvalidArgument
	}
	if err := validateTradeFilter(filterType); err != nil {
		return nil, ulid.ULID{}, err
	}
	if limit <= 0 {
		limit = defaultListLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	trades := make([]domain.Trade, 0, len(r.byID))
	for _, trade := range r.byID {
		if !matchTradeFilter(trade, filterType, filterValue) {
			continue
		}
		trades = append(trades, trade)
	}

	sortTradesDesc(trades)

	if cursor >= len(trades) {
		return []domain.Trade{}, ulid.ULID{}, nil
	}

	end := cursor + limit
	if end > len(trades) {
		end = len(trades)
	}

	result := trades[cursor:end]
	var next ulid.ULID
	if end < len(trades) {
		next = result[len(result)-1].ID
	}

	return result, next, nil
}

func (r *tradeRepo) ListByStock(stockCode string, limit int) ([]domain.Trade, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	trades := make([]domain.Trade, 0)
	for _, trade := range r.byID {
		if trade.Stock.Code != stockCode {
			continue
		}
		trades = append(trades, trade)
	}

	sortTradesDesc(trades)

	if len(trades) > limit {
		trades = trades[:limit]
	}

	return trades, nil
}

func sortTradesDesc(trades []domain.Trade) {
	sort.Slice(trades, func(i, j int) bool {
		if trades[i].CreatedAt.Equal(trades[j].CreatedAt) {
			return bytes.Compare(trades[i].ID[:], trades[j].ID[:]) > 0
		}
		return trades[i].CreatedAt.After(trades[j].CreatedAt)
	})
}

func validateTradeFilter(filterType string) error {
	switch filterType {
	case "", "stockCode", "buyOrderId", "sellOrderId", "userId", "userID":
		return nil
	default:
		return ErrInvalidArgument
	}
}

func matchTradeFilter(t domain.Trade, filterType, filterValue string) bool {
	switch filterType {
	case "":
		return true
	case "stockCode":
		return t.Stock.Code == filterValue
	case "buyOrderId":
		return t.BuyOrder.ID.String() == filterValue
	case "sellOrderId":
		return t.SellOrder.ID.String() == filterValue
	case "userId", "userID":
		return t.BuyOrder.UserID == filterValue || t.SellOrder.UserID == filterValue
	default:
		return false
	}
}
