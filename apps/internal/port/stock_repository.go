package port

import "github.com/bowodev/trading-backend/apps/internal/domain"

type IStockRepository interface {
	Save(s *domain.Stock) error
	FindByCode(code string) (*domain.Stock, error)
}
