package port

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/oklog/ulid/v2"
)

type ITradeRepository interface {
	Save(t *domain.Trade) error
	List(filterType, filterValue string, cursor ulid.ULID, limit int) (trades []domain.Trade, next ulid.ULID, err error)
	ListByStock(stockCode string, limit int) (trades []domain.Trade, err error)
}
