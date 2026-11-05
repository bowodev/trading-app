package memdb

import (
	"sync"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
)

type stockRepo struct {
	mu     *sync.RWMutex
	byCode map[string]domain.Stock
}

func NewStockRepo() port.IStockRepository {
	return &stockRepo{
		mu:     &sync.RWMutex{},
		byCode: make(map[string]domain.Stock),
	}
}

func (r *stockRepo) Save(s *domain.Stock) error {
	if s == nil || s.Code == "" {
		return ErrInvalidData
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.byCode[s.Code] = *s
	return nil
}

func (r *stockRepo) FindByCode(code string) (*domain.Stock, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stock, found := r.byCode[code]
	if !found {
		return nil, ErrDataNotFound
	}

	return &stock, nil
}
