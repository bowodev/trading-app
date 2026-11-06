package applications

import (
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
)

type ListOrdersQuery struct {
	UserID    string
	StockCode string
	Status    domain.OrderStatusType
	Cursor    string
	Limit     int
}

type ListOrders struct {
	orders port.IOrderRepository
}

func NewListOrders(orders port.IOrderRepository) *ListOrders {
	return &ListOrders{orders: orders}
}

func (uc *ListOrders) Execute(query ListOrdersQuery) (Page[OrderView], error) {
	if query.Status != "" && !isValidOrderStatus(query.Status) {
		return Page[OrderView]{}, ErrInvalidArgument
	}

	cursor, err := parseCursor(query.Cursor)
	if err != nil {
		return Page[OrderView]{}, err
	}

	limit := normalizeLimit(query.Limit, defaultLimit)
	orders, next, err := uc.orders.List(port.OrderFilter{
		UserID:    query.UserID,
		StockCode: query.StockCode,
		Status:    query.Status,
	}, cursor, limit)
	if err != nil {
		return Page[OrderView]{}, mapInvalidArgument(err)
	}

	data := make([]OrderView, 0, len(orders))
	for _, order := range orders {
		data = append(data, newOrderView(order))
	}

	return Page[OrderView]{
		NextPointer: encodeCursor(next),
		Limit:       limit,
		Data:        data,
	}, nil
}

func isValidOrderStatus(status domain.OrderStatusType) bool {
	switch status {
	case domain.OrderStatusNew, domain.OrderStatusPartiallyMatched, domain.OrderStatusMatch:
		return true
	default:
		return false
	}
}
