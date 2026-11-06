package port

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/oklog/ulid/v2"
)

// OrderBookChange is applied atomically by IOrderBookRepository.Apply.
type OrderBookChange struct {
	Upserts []domain.Order
	Removes []ulid.ULID
}

type IOrderBookRepository interface {
	Get(stockId ulid.ULID) (*domain.OrderBook, error)
	Add(order *domain.Order) error
	Remove(stockId, orderId ulid.ULID) error
	Update(order *domain.Order) error

	Apply(stock domain.Stock, change OrderBookChange) error
	Snapshot(stockId ulid.ULID, depth int) (bids []domain.Order, asks []domain.Order, err error)

	BestBids(stockId ulid.ULID, limit int) ([]domain.Order, error)
	BestAsks(stockId ulid.ULID, limit int) ([]domain.Order, error)
}
