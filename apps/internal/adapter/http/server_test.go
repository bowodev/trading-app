package httpadapter

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bowodev/trading-backend/apps/internal/adapter/matching_engine"
	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/applications"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
)

type discardPublisher struct{}

func (discardPublisher) Publish(port.Event) error { return nil }

type serverFixture struct {
	handler *Server
	stocks  port.IStockRepository
	orders  port.IOrderRepository
	trades  port.ITradeRepository
	books   port.IOrderBookRepository
	stock   domain.Stock
}

func newServerFixture(t *testing.T) *serverFixture {
	t.Helper()

	stocks := memdb.NewStockRepo()
	orders := memdb.NewOrderRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	marketData := memdb.NewMarketDataRepo()

	engine := matching_engine.New(books)
	t.Cleanup(engine.Close)

	handler := New(
		applications.NewPlaceOrder(stocks, orders, trades, engine, discardPublisher{}),
		applications.NewListOrders(orders),
		applications.NewTradeHistory(trades),
		applications.NewMarketSnapshot(stocks, books, trades, marketData),
	)

	stock := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	if err := stocks.Save(&stock); err != nil {
		t.Fatalf("seed stock: %v", err)
	}

	return &serverFixture{
		handler: handler,
		stocks:  stocks,
		orders:  orders,
		trades:  trades,
		books:   books,
		stock:   stock,
	}
}

func (f *serverFixture) request(t *testing.T, method, path, userID string, body any) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var payload T
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}

	return payload
}

type placeOrderEnvelope struct {
	Message string `json:"message"`
	Data    struct {
		Order  orderResponse   `json:"order"`
		Trades []tradeResponse `json:"trades"`
	} `json:"data"`
}

func TestPlaceOrderHandler(t *testing.T) {
	f := newServerFixture(t)

	rec := f.request(t, http.MethodPost, "/api/v1/orders", "user-1", map[string]any{
		"stockCode": "BMRI",
		"side":      "BUY",
		"price":     "999",
		"qty":       100,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", got)
	}

	payload := decodeBody[placeOrderEnvelope](t, rec)
	if payload.Message != "order placed" {
		t.Fatalf("message = %q", payload.Message)
	}
	if payload.Data.Order.ID == "" || payload.Data.Order.Status != string(domain.OrderStatusNew) {
		t.Fatalf("order = %+v", payload.Data.Order)
	}
	if len(payload.Data.Trades) != 0 {
		t.Fatalf("trades = %+v, want none", payload.Data.Trades)
	}

	book, err := f.books.Get(f.stock.ID)
	if err != nil {
		t.Fatalf("Get book: %v", err)
	}
	bids := book.Bids["999"]
	if len(bids) != 1 || bids[0].Qty != 100 {
		t.Fatalf("bids = %+v, want qty 100", bids)
	}
}

func TestPlaceOrderHandlerUserIdFallback(t *testing.T) {
	f := newServerFixture(t)

	rec := f.request(t, http.MethodPost, "/api/v1/orders", "", map[string]any{
		"userId":    "user-body",
		"stockCode": "BMRI",
		"side":      "SELL",
		"price":     "1000",
		"qty":       1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	payload := decodeBody[placeOrderEnvelope](t, rec)
	if payload.Data.Order.UserID != "user-body" {
		t.Fatalf("userId = %q, want user-body", payload.Data.Order.UserID)
	}
}

func TestPlaceOrderHandlerAnonymousFallback(t *testing.T) {
	f := newServerFixture(t)

	rec := f.request(t, http.MethodPost, "/api/v1/orders", "", map[string]any{
		"stockCode": "BMRI",
		"side":      "BUY",
		"price":     "1000",
		"qty":       1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	payload := decodeBody[placeOrderEnvelope](t, rec)
	if !strings.HasPrefix(payload.Data.Order.UserID, defaultUserID+"-") {
		t.Fatalf("userId = %q, want an %s-* identity", payload.Data.Order.UserID, defaultUserID)
	}
}

func TestAnonymousOrdersCanMatch(t *testing.T) {
	f := newServerFixture(t)

	sell := f.request(t, http.MethodPost, "/api/v1/orders", "", map[string]any{
		"stockCode": "BMRI",
		"side":      "SELL",
		"price":     "1000",
		"qty":       10,
	})
	if sell.Code != http.StatusCreated {
		t.Fatalf("sell status = %d; body = %s", sell.Code, sell.Body.String())
	}

	buy := f.request(t, http.MethodPost, "/api/v1/orders", "", map[string]any{
		"stockCode": "BMRI",
		"side":      "BUY",
		"price":     "1000",
		"qty":       10,
	})
	if buy.Code != http.StatusCreated {
		t.Fatalf("buy status = %d; body = %s", buy.Code, buy.Body.String())
	}

	payload := decodeBody[placeOrderEnvelope](t, buy)
	if len(payload.Data.Trades) != 1 || payload.Data.Trades[0].Quantity != 10 {
		t.Fatalf("trades = %+v, want one anonymous trade of 10", payload.Data.Trades)
	}
}

func TestListOrdersWithoutIdentityReturnsAll(t *testing.T) {
	f := newServerFixture(t)

	for _, userID := range []string{"user-1", "user-2"} {
		rec := f.request(t, http.MethodPost, "/api/v1/orders", userID, map[string]any{
			"stockCode": "BMRI",
			"side":      "BUY",
			"price":     "900",
			"qty":       1,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("place order %s: status = %d; body = %s", userID, rec.Code, rec.Body.String())
		}
	}

	rec := f.request(t, http.MethodGet, "/api/v1/orders", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	page := decodeBody[struct {
		Data struct {
			Data []orderResponse `json:"data"`
		} `json:"data"`
	}](t, rec)
	if len(page.Data.Data) != 2 {
		t.Fatalf("orders = %d, want 2 from every user", len(page.Data.Data))
	}
}

func TestTradeHistoryWithoutIdentityReturnsAll(t *testing.T) {
	f := newServerFixture(t)

	sell := f.request(t, http.MethodPost, "/api/v1/orders", "seller", map[string]any{
		"stockCode": "BMRI",
		"side":      "SELL",
		"price":     "1000",
		"qty":       10,
	})
	if sell.Code != http.StatusCreated {
		t.Fatalf("sell status = %d; body = %s", sell.Code, sell.Body.String())
	}

	buy := f.request(t, http.MethodPost, "/api/v1/orders", "buyer", map[string]any{
		"stockCode": "BMRI",
		"side":      "BUY",
		"price":     "1000",
		"qty":       10,
	})
	if buy.Code != http.StatusCreated {
		t.Fatalf("buy status = %d; body = %s", buy.Code, buy.Body.String())
	}

	history := f.request(t, http.MethodGet, "/api/v1/orders/history", "", nil)
	if history.Code != http.StatusOK {
		t.Fatalf("history status = %d; body = %s", history.Code, history.Body.String())
	}

	page := decodeBody[struct {
		Data struct {
			Data []tradeResponse `json:"data"`
		} `json:"data"`
	}](t, history)
	if len(page.Data.Data) != 1 {
		t.Fatalf("trades = %d, want 1", len(page.Data.Data))
	}
}

func TestPlaceOrderHandlerMatchAndHistory(t *testing.T) {
	f := newServerFixture(t)

	sell := f.request(t, http.MethodPost, "/api/v1/orders", "seller", map[string]any{
		"stockCode": "BMRI",
		"side":      "SELL",
		"price":     "1000",
		"qty":       10,
	})
	if sell.Code != http.StatusCreated {
		t.Fatalf("sell status = %d; body = %s", sell.Code, sell.Body.String())
	}

	buy := f.request(t, http.MethodPost, "/api/v1/orders", "buyer", map[string]any{
		"stockCode": "BMRI",
		"side":      "BUY",
		"price":     "1000",
		"qty":       10,
	})
	if buy.Code != http.StatusCreated {
		t.Fatalf("buy status = %d; body = %s", buy.Code, buy.Body.String())
	}

	payload := decodeBody[placeOrderEnvelope](t, buy)
	if payload.Data.Order.Status != string(domain.OrderStatusMatch) {
		t.Fatalf("status = %q, want MATCHED", payload.Data.Order.Status)
	}
	if len(payload.Data.Trades) != 1 || payload.Data.Trades[0].Quantity != 10 {
		t.Fatalf("trades = %+v, want one trade of 10", payload.Data.Trades)
	}

	history := f.request(t, http.MethodGet, "/api/v1/orders/history", "buyer", nil)
	if history.Code != http.StatusOK {
		t.Fatalf("history status = %d; body = %s", history.Code, history.Body.String())
	}

	historyPayload := decodeBody[struct {
		Message string `json:"message"`
		Data    struct {
			NextPointer string          `json:"nextPointer"`
			Limit       int             `json:"limit"`
			Data        []tradeResponse `json:"data"`
		} `json:"data"`
	}](t, history)
	if historyPayload.Message != "success" {
		t.Fatalf("message = %q", historyPayload.Message)
	}
	if len(historyPayload.Data.Data) != 1 {
		t.Fatalf("trades = %+v, want 1", historyPayload.Data.Data)
	}
	if historyPayload.Data.Data[0].BuyOrderID != payload.Data.Order.ID {
		t.Fatalf("trade buy order = %q, want %q", historyPayload.Data.Data[0].BuyOrderID, payload.Data.Order.ID)
	}
}

func TestListOrdersHandlerPagination(t *testing.T) {
	f := newServerFixture(t)

	for i := 0; i < 3; i++ {
		rec := f.request(t, http.MethodPost, "/api/v1/orders", "user-1", map[string]any{
			"stockCode": "BMRI",
			"side":      "BUY",
			"price":     "900",
			"qty":       1,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("place order %d: status = %d; body = %s", i, rec.Code, rec.Body.String())
		}
	}

	type ordersPage struct {
		NextPointer string          `json:"nextPointer"`
		Limit       int             `json:"limit"`
		Data        []orderResponse `json:"data"`
	}
	type ordersEnvelope struct {
		Message string     `json:"message"`
		Data    ordersPage `json:"data"`
	}

	first := f.request(t, http.MethodGet, "/api/v1/orders?limit=2", "user-1", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d; body = %s", first.Code, first.Body.String())
	}
	firstPayload := decodeBody[ordersEnvelope](t, first)
	if len(firstPayload.Data.Data) != 2 {
		t.Fatalf("first page len = %d, want 2", len(firstPayload.Data.Data))
	}
	if firstPayload.Data.NextPointer == "" {
		t.Fatal("nextPointer is empty, want cursor")
	}

	second := f.request(t, http.MethodGet, "/api/v1/orders?limit=2&cursor="+firstPayload.Data.NextPointer, "user-1", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d; body = %s", second.Code, second.Body.String())
	}
	secondPayload := decodeBody[ordersEnvelope](t, second)
	if len(secondPayload.Data.Data) != 1 {
		t.Fatalf("second page len = %d, want 1", len(secondPayload.Data.Data))
	}

	filtered := f.request(t, http.MethodGet, "/api/v1/orders?stockCode=BBCA", "user-1", nil)
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered status = %d; body = %s", filtered.Code, filtered.Body.String())
	}
	if got := len(decodeBody[ordersEnvelope](t, filtered).Data.Data); got != 0 {
		t.Fatalf("filtered orders = %d, want 0", got)
	}
}

func TestMarketSnapshotHandler(t *testing.T) {
	f := newServerFixture(t)

	buy := f.request(t, http.MethodPost, "/api/v1/orders", "buyer", map[string]any{
		"stockCode": "BMRI",
		"side":      "BUY",
		"price":     "999",
		"qty":       200,
	})
	if buy.Code != http.StatusCreated {
		t.Fatalf("buy status = %d; body = %s", buy.Code, buy.Body.String())
	}

	sell := f.request(t, http.MethodPost, "/api/v1/orders", "seller", map[string]any{
		"stockCode": "BMRI",
		"side":      "SELL",
		"price":     "1001",
		"qty":       300,
	})
	if sell.Code != http.StatusCreated {
		t.Fatalf("sell status = %d; body = %s", sell.Code, sell.Body.String())
	}

	rec := f.request(t, http.MethodGet, "/api/v1/snapshots?stockCode=BMRI&showTradeLimit=5", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}

	payload := decodeBody[struct {
		Message string           `json:"message"`
		Data    snapshotResponse `json:"data"`
	}](t, rec)
	if payload.Message != "success" {
		t.Fatalf("message = %q", payload.Message)
	}
	if payload.Data.StockCode != "BMRI" {
		t.Fatalf("stockCode = %q", payload.Data.StockCode)
	}
	if len(payload.Data.OrderBook.Bids) != 1 || payload.Data.OrderBook.Bids[0].Price != "999" || payload.Data.OrderBook.Bids[0].Qty != 200 {
		t.Fatalf("bids = %+v", payload.Data.OrderBook.Bids)
	}
	if len(payload.Data.OrderBook.Asks) != 1 || payload.Data.OrderBook.Asks[0].Price != "1001" || payload.Data.OrderBook.Asks[0].Qty != 300 {
		t.Fatalf("asks = %+v", payload.Data.OrderBook.Asks)
	}
	if len(payload.Data.Trades) != 0 {
		t.Fatalf("trades = %+v, want none", payload.Data.Trades)
	}
}

func TestHandlerErrors(t *testing.T) {
	f := newServerFixture(t)

	tests := []struct {
		name   string
		method string
		path   string
		userID string
		body   any
		want   int
	}{
		{
			name:   "unknown field",
			method: http.MethodPost,
			path:   "/api/v1/orders",
			userID: "user-1",
			body:   map[string]any{"stockCode": "BMRI", "side": "BUY", "price": "1000", "qty": 1, "extra": true},
			want:   http.StatusBadRequest,
		},
		{
			name:   "bad price",
			method: http.MethodPost,
			path:   "/api/v1/orders",
			userID: "user-1",
			body:   map[string]any{"stockCode": "BMRI", "side": "BUY", "price": "abc", "qty": 1},
			want:   http.StatusBadRequest,
		},
		{
			name:   "invalid side",
			method: http.MethodPost,
			path:   "/api/v1/orders",
			userID: "user-1",
			body:   map[string]any{"stockCode": "BMRI", "side": "HOLD", "price": "1000", "qty": 1},
			want:   http.StatusBadRequest,
		},
		{
			name:   "unknown stock",
			method: http.MethodPost,
			path:   "/api/v1/orders",
			userID: "user-1",
			body:   map[string]any{"stockCode": "NONE", "side": "BUY", "price": "1000", "qty": 1},
			want:   http.StatusNotFound,
		},
		{
			name:   "list invalid status",
			method: http.MethodGet,
			path:   "/api/v1/orders?status=HOLD",
			userID: "user-1",
			want:   http.StatusBadRequest,
		},
		{
			name:   "list invalid cursor",
			method: http.MethodGet,
			path:   "/api/v1/orders?cursor=bad",
			userID: "user-1",
			want:   http.StatusBadRequest,
		},
		{
			name:   "snapshot without stock",
			method: http.MethodGet,
			path:   "/api/v1/snapshots",
			want:   http.StatusBadRequest,
		},
		{
			name:   "snapshot unknown stock",
			method: http.MethodGet,
			path:   "/api/v1/snapshots?stockCode=NONE",
			want:   http.StatusNotFound,
		},
		{
			name:   "snapshot bad limit",
			method: http.MethodGet,
			path:   "/api/v1/snapshots?stockCode=BMRI&showTradeLimit=abc",
			want:   http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.request(t, tt.method, tt.path, tt.userID, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tt.want, rec.Body.String())
			}

			payload := decodeBody[envelope](t, rec)
			if payload.Message == "" {
				t.Fatalf("error message is empty")
			}
		})
	}
}

func TestHandlerRouting(t *testing.T) {
	f := newServerFixture(t)

	health := f.request(t, http.MethodGet, "/healthz", "", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	notFound := f.request(t, http.MethodGet, "/nope", "", nil)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want 404", notFound.Code)
	}

	wrongMethod := f.request(t, http.MethodPut, "/api/v1/orders", "", nil)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d, want 405", wrongMethod.Code)
	}
}
