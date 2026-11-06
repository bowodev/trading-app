package memdb

import (
	"sync"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

type marketDataRepo struct {
	mu     *sync.RWMutex
	byCode map[string]port.MarketData
}

func NewMarketDataRepo() port.IMarketDataRepository {
	return &marketDataRepo{
		mu:     &sync.RWMutex{},
		byCode: make(map[string]port.MarketData),
	}
}

func (r *marketDataRepo) Save(data port.MarketData) error {
	if data.StockCode == "" {
		return ErrInvalidData
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.byCode[data.StockCode] = data
	return nil
}

func (r *marketDataRepo) Get(stockCode string) (port.MarketData, error) {
	if stockCode == "" {
		return port.MarketData{}, ErrInvalidArgument
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	data, found := r.byCode[stockCode]
	if !found {
		return port.MarketData{}, ErrDataNotFound
	}

	return data, nil
}

func (r *marketDataRepo) SetLast(stockCode string, last decimal.Decimal, updatedAt time.Time) (port.MarketData, error) {
	if stockCode == "" {
		return port.MarketData{}, ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	data, found := r.byCode[stockCode]
	if !found {
		return port.MarketData{}, ErrDataNotFound
	}

	data.Last = last
	data.UpdatedAt = updatedAt
	r.byCode[stockCode] = data

	return data, nil
}
