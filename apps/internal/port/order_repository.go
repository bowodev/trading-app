package port

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/oklog/ulid/v2"
)

type OrderFilter struct {
	UserID    string
	StockCode string
	Status    domain.OrderStatusType
}

type IOrderRepository interface {
	Save(o *domain.Order) error
	FindByID(id ulid.ULID) (*domain.Order, error)
	Update(o *domain.Order) error
	List(filter OrderFilter, cursor ulid.ULID, limit int) (orders []domain.Order, next ulid.ULID, err error)
}
