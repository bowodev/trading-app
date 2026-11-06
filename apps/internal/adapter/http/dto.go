package httpadapter

import (
	"time"

	"github.com/bowodev/trading-backend/apps/internal/applications"
)

type orderResponse struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	StockCode string    `json:"stockCode"`
	Side      string    `json:"side"`
	Status    string    `json:"status"`
	Price     string    `json:"price"`
	Qty       int       `json:"qty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type tradeResponse struct {
	ID          string    `json:"id"`
	StockCode   string    `json:"stockCode"`
	Price       string    `json:"price"`
	Quantity    int       `json:"quantity"`
	BuyOrderID  string    `json:"buyOrderId"`
	SellOrderID string    `json:"sellOrderId"`
	CreatedAt   time.Time `json:"createdAt"`
}

type priceResponse struct {
	Last   string `json:"last"`
	Change string `json:"change"`
	Volume int    `json:"volume"`
}

type orderBookLevelResponse struct {
	Price string `json:"price"`
	Qty   int    `json:"qty"`
}

type orderBookResponse struct {
	Bids []orderBookLevelResponse `json:"bids"`
	Asks []orderBookLevelResponse `json:"asks"`
}

type snapshotResponse struct {
	StockCode string            `json:"stockCode"`
	Price     priceResponse     `json:"price"`
	OrderBook orderBookResponse `json:"orderBook"`
	Trades    []tradeResponse   `json:"trades"`
}

type pageResponse[T any] struct {
	NextPointer string `json:"nextPointer"`
	Limit       int    `json:"limit"`
	Data        []T    `json:"data"`
}

type placeOrderResponse struct {
	Order  orderResponse   `json:"order"`
	Trades []tradeResponse `json:"trades"`
}

type envelope struct {
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func newOrderResponse(view applications.OrderView) orderResponse {
	return orderResponse{
		ID:        view.ID,
		UserID:    view.UserID,
		StockCode: view.StockCode,
		Side:      string(view.Side),
		Status:    string(view.Status),
		Price:     view.Price,
		Qty:       view.Qty,
		CreatedAt: view.CreatedAt,
		UpdatedAt: view.UpdatedAt,
	}
}

func newOrderResponses(views []applications.OrderView) []orderResponse {
	responses := make([]orderResponse, 0, len(views))
	for _, view := range views {
		responses = append(responses, newOrderResponse(view))
	}

	return responses
}

func newTradeResponse(view applications.TradeView) tradeResponse {
	return tradeResponse{
		ID:          view.ID,
		StockCode:   view.StockCode,
		Price:       view.Price,
		Quantity:    view.Quantity,
		BuyOrderID:  view.BuyOrderID,
		SellOrderID: view.SellOrderID,
		CreatedAt:   view.CreatedAt,
	}
}

func newTradeResponses(views []applications.TradeView) []tradeResponse {
	responses := make([]tradeResponse, 0, len(views))
	for _, view := range views {
		responses = append(responses, newTradeResponse(view))
	}

	return responses
}

func newOrderPageResponse(page applications.Page[applications.OrderView]) pageResponse[orderResponse] {
	return pageResponse[orderResponse]{
		NextPointer: page.NextPointer,
		Limit:       page.Limit,
		Data:        newOrderResponses(page.Data),
	}
}

func newTradePageResponse(page applications.Page[applications.TradeView]) pageResponse[tradeResponse] {
	return pageResponse[tradeResponse]{
		NextPointer: page.NextPointer,
		Limit:       page.Limit,
		Data:        newTradeResponses(page.Data),
	}
}

func newSnapshotResponse(view applications.SnapshotView) snapshotResponse {
	return snapshotResponse{
		StockCode: view.StockCode,
		Price: priceResponse{
			Last:   view.Price.Last,
			Change: view.Price.Change,
			Volume: view.Price.Volume,
		},
		OrderBook: orderBookResponse{
			Bids: newLevelResponses(view.OrderBook.Bids),
			Asks: newLevelResponses(view.OrderBook.Asks),
		},
		Trades: newTradeResponses(view.Trades),
	}
}

func newLevelResponses(views []applications.OrderBookLevelView) []orderBookLevelResponse {
	responses := make([]orderBookLevelResponse, 0, len(views))
	for _, view := range views {
		responses = append(responses, orderBookLevelResponse{
			Price: view.Price,
			Qty:   view.Qty,
		})
	}

	return responses
}
