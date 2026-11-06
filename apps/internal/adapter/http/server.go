package httpadapter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bowodev/trading-backend/apps/internal/applications"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

const (
	maxBodyBytes  = 1 << 20
	defaultUserID = "anonymous"
)

type Server struct {
	placeOrder     *applications.PlaceOrder
	listOrders     *applications.ListOrders
	tradeHistory   *applications.TradeHistory
	marketSnapshot *applications.MarketSnapshot

	mux *http.ServeMux
}

func New(
	placeOrder *applications.PlaceOrder,
	listOrders *applications.ListOrders,
	tradeHistory *applications.TradeHistory,
	marketSnapshot *applications.MarketSnapshot,
) *Server {
	server := &Server{
		placeOrder:     placeOrder,
		listOrders:     listOrders,
		tradeHistory:   tradeHistory,
		marketSnapshot: marketSnapshot,
		mux:            http.NewServeMux(),
	}

	server.mux.HandleFunc("GET /healthz", server.handleHealth)
	server.mux.HandleFunc("POST /api/v1/orders", server.handlePlaceOrder)
	server.mux.HandleFunc("GET /api/v1/orders", server.handleListOrders)
	server.mux.HandleFunc("GET /api/v1/orders/history", server.handleTradeHistory)
	server.mux.HandleFunc("GET /api/v1/snapshots", server.handleMarketSnapshot)

	return server
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Handle registers an additional route, such as the WebSocket endpoint, on the
// same mux. Call it before the server starts serving.
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

type placeOrderRequest struct {
	UserID    string `json:"userId"`
	StockCode string `json:"stockCode"`
	Side      string `json:"side"`
	Price     string `json:"price"`
	Qty       int    `json:"qty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, envelope{Message: "ok"})
}

func (s *Server) handlePlaceOrder(w http.ResponseWriter, r *http.Request) {
	var body placeOrderRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	price, err := decimal.NewFromString(strings.TrimSpace(body.Price))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid argument")
		return
	}

	result, err := s.placeOrder.Execute(applications.PlaceOrderCommand{
		UserID:    placeOrderUserID(r, body.UserID),
		StockCode: strings.TrimSpace(body.StockCode),
		Side:      domain.OrderSideType(strings.ToUpper(strings.TrimSpace(body.Side))),
		Price:     price,
		Qty:       body.Qty,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, envelope{
		Message: "order placed",
		Data: placeOrderResponse{
			Order:  newOrderResponse(result.Order),
			Trades: newTradeResponses(result.Trades),
		},
	})
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit, err := intQuery(query.Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid argument")
		return
	}

	page, err := s.listOrders.Execute(applications.ListOrdersQuery{
		UserID:    userID(r, query.Get("userId")),
		StockCode: strings.TrimSpace(query.Get("stockCode")),
		Status:    domain.OrderStatusType(strings.ToUpper(strings.TrimSpace(query.Get("status")))),
		Cursor:    query.Get("cursor"),
		Limit:     limit,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		Message: "success",
		Data:    newOrderPageResponse(page),
	})
}

func (s *Server) handleTradeHistory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit, err := intQuery(query.Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid argument")
		return
	}

	page, err := s.tradeHistory.Execute(applications.TradeHistoryQuery{
		UserID: userID(r, query.Get("userId")),
		Cursor: query.Get("cursor"),
		Limit:  limit,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		Message: "success",
		Data:    newTradePageResponse(page),
	})
}

func (s *Server) handleMarketSnapshot(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit, err := intQuery(query.Get("showTradeLimit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid argument")
		return
	}

	snapshot, err := s.marketSnapshot.Execute(applications.MarketSnapshotQuery{
		StockCode:      strings.TrimSpace(query.Get("stockCode")),
		ShowTradeLimit: limit,
	})
	if err != nil {
		writeApplicationError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, envelope{
		Message: "success",
		Data:    newSnapshotResponse(snapshot),
	})
}

// userID resolves the caller identity from the header first, then from an
// optional request field. The API has no auth layer yet. An empty identity
// means "no user filter" for order and trade listing.
func userID(r *http.Request, fallback string) string {
	if id := strings.TrimSpace(r.Header.Get("X-User-ID")); id != "" {
		return id
	}

	return strings.TrimSpace(fallback)
}

// placeOrderUserID keeps the minimal create-order payload working: an
// anonymous caller becomes a distinct, listable principal instead of a 400.
// A unique suffix avoids accidental self-trade prevention when the same
// unauthenticated client submits both sides.
func placeOrderUserID(r *http.Request, fallback string) string {
	if id := userID(r, fallback); id != "" {
		return id
	}

	return defaultUserID + "-" + ulid.Make().String()
}

func intQuery(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}

	return strconv.Atoi(raw)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}

	return nil
}

func writeApplicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, applications.ErrInvalidArgument), errors.Is(err, port.ErrInvalidArgument):
		writeError(w, http.StatusBadRequest, "invalid argument")
	case errors.Is(err, applications.ErrStockNotFound):
		writeError(w, http.StatusNotFound, "stock not found")
	case errors.Is(err, port.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, applications.ErrMatchingFailed):
		writeError(w, http.StatusInternalServerError, "matching failed")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, envelope{Message: message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
