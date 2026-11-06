package applications

import "github.com/bowodev/trading-backend/apps/internal/port"

type TradeHistoryQuery struct {
	UserID string
	Cursor string
	Limit  int
}

type TradeHistory struct {
	trades port.ITradeRepository
}

func NewTradeHistory(trades port.ITradeRepository) *TradeHistory {
	return &TradeHistory{trades: trades}
}

func (uc *TradeHistory) Execute(query TradeHistoryQuery) (Page[TradeView], error) {
	cursor, err := parseCursor(query.Cursor)
	if err != nil {
		return Page[TradeView]{}, err
	}

	filterType, filterValue := "", ""
	if query.UserID != "" {
		filterType, filterValue = "userId", query.UserID
	}

	limit := normalizeLimit(query.Limit, defaultLimit)
	trades, next, err := uc.trades.List(filterType, filterValue, cursor, limit)
	if err != nil {
		return Page[TradeView]{}, mapInvalidArgument(err)
	}

	return Page[TradeView]{
		NextPointer: encodeCursor(next),
		Limit:       limit,
		Data:        newTradeViews(trades),
	}, nil
}
