package port

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/oklog/ulid/v2"
)

type IOrderBookRepository interface {
	Get(stockId ulid.ULID) (*domain.OrderBook, error)
	Add(order *domain.Order) error
	Remove(stockId, orderId ulid.ULID) error
	Update(order *domain.Order) error

	BestBids(stockId ulid.ULID, limit int) ([]domain.Order, error)
	BestAsks(stockId ulid.ULID, limit int) ([]domain.Order, error)
}
