package port

import (
	"time"

	"github.com/shopspring/decimal"
)

type MarketData struct {
	StockCode string
	Last      decimal.Decimal
	PrevClose decimal.Decimal
	Volume    int
	UpdatedAt time.Time
}

type IMarketDataRepository interface {
	Get(stockCode string) (MarketData, error)
	Save(data MarketData) error
	// SetLast updates only price and time so a concurrent trade keeps its volume.
	SetLast(stockCode string, last decimal.Decimal, updatedAt time.Time) (MarketData, error)
}
