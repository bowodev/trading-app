package port

import "github.com/bowodev/trading-backend/apps/internal/domain"

// MatchResult carries the outcome of matching one incoming order.
// AffectedOrders are resting orders whose Qty must be read as the remaining
// quantity after the fill.
type MatchResult struct {
	FilledQty      int
	Trades         []domain.Trade
	AffectedOrders []domain.Order
}

type IMatchingEngine interface {
	Match(order domain.Order) (MatchResult, error)
}
