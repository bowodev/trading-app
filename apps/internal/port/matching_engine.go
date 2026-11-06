package port

import "github.com/bowodev/trading-backend/apps/internal/domain"

// MatchResult carries the outcome; AffectedOrders hold remaining quantity after the fill.
type MatchResult struct {
	FilledQty      int
	Trades         []domain.Trade
	AffectedOrders []domain.Order
}

type IMatchingEngine interface {
	Match(order domain.Order) (MatchResult, error)
}
