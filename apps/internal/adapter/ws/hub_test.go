package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/applications"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/coder/websocket"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

type hubFixture struct {
	hub    *Hub
	server *httptest.Server
	stock  domain.Stock
	books  port.IOrderBookRepository
}

func newHubFixture(t *testing.T) *hubFixture {
	t.Helper()

	return newHubFixtureWithOrigins(t, nil)
}

func newHubFixtureWithOrigins(t *testing.T, origins []string) *hubFixture {
	t.Helper()

	stocks := memdb.NewStockRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	marketData := memdb.NewMarketDataRepo()

	stock := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	if err := stocks.Save(&stock); err != nil {
		t.Fatalf("seed stock: %v", err)
	}

	now := time.Now().UTC()
	resting := domain.Order{
		ID:        ulid.Make(),
		UserID:    "user-1",
		Stock:     stock,
		Side:      domain.OrderSideBuy,
		Status:    domain.OrderStatusNew,
		Price:     decimal.RequireFromString("999"),
		Qty:       100,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := books.Add(&resting); err != nil {
		t.Fatalf("seed book: %v", err)
	}

	if err := marketData.Save(port.MarketData{
		StockCode: "BMRI",
		Last:      decimal.RequireFromString("1005"),
		PrevClose: decimal.RequireFromString("1000"),
		Volume:    1800,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed market data: %v", err)
	}

	snapshots := applications.NewMarketSnapshot(stocks, books, trades, marketData)
	hub := NewHub(snapshots, origins)
	server := httptest.NewServer(hub)

	t.Cleanup(func() {
		hub.Close()
		server.Close()
	})

	return &hubFixture{
		hub:    hub,
		server: server,
		stock:  stock,
		books:  books,
	}
}

func (f *hubFixture) dial(t *testing.T, query string) *websocket.Conn {
	t.Helper()

	url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/" + query
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.CloseNow()
	})

	return conn
}

func TestHubOriginPolicy(t *testing.T) {
	const foreignOrigin = "https://app.bruno.io"

	dialWithOrigin := func(t *testing.T, f *hubFixture) error {
		t.Helper()

		url := "ws" + strings.TrimPrefix(f.server.URL, "http") + "/"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": {foreignOrigin}},
		})
		if err == nil {
			t.Cleanup(func() {
				_ = conn.CloseNow()
			})
		}

		return err
	}

	t.Run("wildcard accepts a desktop client origin", func(t *testing.T) {
		f := newHubFixtureWithOrigins(t, []string{"*"})
		if err := dialWithOrigin(t, f); err != nil {
			t.Fatalf("dial with foreign origin: %v", err)
		}
	})

	t.Run("empty patterns reject a foreign origin", func(t *testing.T) {
		f := newHubFixture(t)
		if err := dialWithOrigin(t, f); err == nil {
			t.Fatal("dial with foreign origin succeeded, want rejection")
		}
	})
}

type testMessage struct {
	Type      string          `json:"type"`
	StockCode string          `json:"stockCode"`
	Data      json.RawMessage `json:"data"`
	Message   string          `json:"message"`
}

func readMessage(t *testing.T, conn *websocket.Conn) testMessage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	typ, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("message type = %v, want text", typ)
	}

	var message testMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode message %q: %v", payload, err)
	}

	return message
}

func sendMessage(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write command: %v", err)
	}
}

func subscribe(t *testing.T, conn *websocket.Conn, channels []string, stockCode, userID string) testMessage {
	t.Helper()

	sendMessage(t, conn, clientCommand{
		Type:      "subscribe",
		Channels:  channels,
		StockCode: stockCode,
		UserID:    userID,
	})

	ack := readMessage(t, conn)
	if ack.Type != "subscribed" {
		t.Fatalf("ack = %+v, want subscribed", ack)
	}

	return ack
}

func decodeData[T any](t *testing.T, message testMessage) T {
	t.Helper()

	var payload T
	if err := json.Unmarshal(message.Data, &payload); err != nil {
		t.Fatalf("decode %s data: %v", message.Type, err)
	}

	return payload
}

func TestHubSubscribesTickerWithInitialValue(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelMarketTicker}, "BMRI", "")

	message := readMessage(t, conn)
	if message.Type != ChannelMarketTicker || message.StockCode != "BMRI" {
		t.Fatalf("message = %+v, want market.ticker for BMRI", message)
	}

	ticker := decodeData[tickerPayload](t, message)
	if ticker.Last != "1005" || ticker.Change != "5" || ticker.Volume != 1800 {
		t.Fatalf("ticker = %+v, want last=1005 change=5 volume=1800", ticker)
	}
}

func TestHubSubscribesOrderBookWithInitialDepth(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelMarketOrderBook}, "BMRI", "")

	message := readMessage(t, conn)
	if message.Type != ChannelMarketOrderBook || message.StockCode != "BMRI" {
		t.Fatalf("message = %+v, want market.orderbook for BMRI", message)
	}

	book := decodeData[orderBookEventPayload](t, message)
	if len(book.Bids) != 1 || book.Bids[0].Price != "999" || book.Bids[0].Qty != 100 {
		t.Fatalf("bids = %+v, want one level 999x100", book.Bids)
	}
}

func TestHubStreamsTradeAndTicker(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelMarketTicker, ChannelMarketTrade}, "BMRI", "")
	readMessage(t, conn)

	now := time.Now().UTC()
	trade := domain.Trade{
		ID:        ulid.Make(),
		Stock:     f.stock,
		Price:     domain.Price(decimal.RequireFromString("1010")),
		Quantity:  25,
		BuyOrder:  domain.Order{ID: ulid.Make(), UserID: "buyer", Stock: f.stock},
		SellOrder: domain.Order{ID: ulid.Make(), UserID: "seller", Stock: f.stock},
		CreatedAt: now,
	}

	if err := f.hub.Publish(port.TradeExecutedEvent{Trade: trade}); err != nil {
		t.Fatalf("publish trade: %v", err)
	}
	if err := f.hub.Publish(port.MarketTickerEvent{
		StockCode: "BMRI",
		Last:      decimal.RequireFromString("1010"),
		Change:    decimal.RequireFromString("10"),
		Volume:    1825,
	}); err != nil {
		t.Fatalf("publish ticker: %v", err)
	}

	tradeMessage := readMessage(t, conn)
	if tradeMessage.Type != ChannelMarketTrade {
		t.Fatalf("message = %+v, want market.trade", tradeMessage)
	}
	tradeData := decodeData[tradePayload](t, tradeMessage)
	if tradeData.ID != trade.ID.String() || tradeData.Quantity != 25 {
		t.Fatalf("trade payload = %+v", tradeData)
	}

	tickerMessage := readMessage(t, conn)
	if tickerMessage.Type != ChannelMarketTicker {
		t.Fatalf("message = %+v, want market.ticker", tickerMessage)
	}
	ticker := decodeData[tickerPayload](t, tickerMessage)
	if ticker.Last != "1010" || ticker.Volume != 1825 {
		t.Fatalf("ticker = %+v, want last=1010 volume=1825", ticker)
	}
}

func TestHubOrderUpdateIsScopedPerUser(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelOrderUpdate}, "BMRI", "user-1")

	for _, userID := range []string{"user-2", "user-1"} {
		order := newOrder(f.stock, userID, domain.OrderStatusNew)
		if err := f.hub.Publish(port.OrderPlacedEvent{Order: order}); err != nil {
			t.Fatalf("publish order: %v", err)
		}
	}

	message := readMessage(t, conn)
	if message.Type != ChannelOrderUpdate {
		t.Fatalf("message = %+v, want order.update", message)
	}
	payload := decodeData[orderPayload](t, message)
	if payload.UserID != "user-1" {
		t.Fatalf("userId = %q, want user-1 (user-2 must be filtered)", payload.UserID)
	}
}

func TestHubOrderUpdatePublishesAffectedRestingOrder(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelOrderUpdate}, "BMRI", "user-1")

	affected := newOrder(f.stock, "user-1", domain.OrderStatusPartiallyMatched)
	affected.Qty = 40
	if err := f.hub.Publish(port.OrderUpdatedEvent{Order: affected}); err != nil {
		t.Fatalf("publish update: %v", err)
	}

	message := readMessage(t, conn)
	payload := decodeData[orderPayload](t, message)
	if payload.Status != string(domain.OrderStatusPartiallyMatched) || payload.Qty != 40 {
		t.Fatalf("payload = %+v, want PARTIALLY_MATCHED qty 40", payload)
	}
}

func TestHubUsesUserFromHandshake(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "?userId=user-9")

	subscribe(t, conn, []string{ChannelOrderUpdate}, "BMRI", "")

	order := newOrder(f.stock, "user-9", domain.OrderStatusNew)
	if err := f.hub.Publish(port.OrderPlacedEvent{Order: order}); err != nil {
		t.Fatalf("publish order: %v", err)
	}

	message := readMessage(t, conn)
	if payload := decodeData[orderPayload](t, message); payload.UserID != "user-9" {
		t.Fatalf("userId = %q, want user-9", payload.UserID)
	}
}

func TestHubRefreshesOrderBookAfterOrder(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelMarketOrderBook}, "BMRI", "")
	readMessage(t, conn)

	ask := newOrder(f.stock, "user-2", domain.OrderStatusNew)
	ask.Side = domain.OrderSideSell
	ask.Price = decimal.RequireFromString("1001")
	ask.Qty = 50
	if err := f.books.Add(&ask); err != nil {
		t.Fatalf("add book order: %v", err)
	}

	if err := f.hub.Publish(port.OrderPlacedEvent{Order: ask}); err != nil {
		t.Fatalf("publish order: %v", err)
	}

	message := readMessage(t, conn)
	if message.Type != ChannelMarketOrderBook {
		t.Fatalf("message = %+v, want market.orderbook", message)
	}
	book := decodeData[orderBookEventPayload](t, message)
	if len(book.Asks) != 1 || book.Asks[0].Price != "1001" || book.Asks[0].Qty != 50 {
		t.Fatalf("asks = %+v, want one level 1001x50", book.Asks)
	}
}

func TestHubUnsubscribeStopsEvents(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelMarketTrade}, "BMRI", "")

	sendMessage(t, conn, clientCommand{Type: "unsubscribe", Channels: []string{ChannelMarketTrade}})
	if ack := readMessage(t, conn); ack.Type != "unsubscribed" {
		t.Fatalf("ack = %+v, want unsubscribed", ack)
	}

	trade := domain.Trade{
		ID:        ulid.Make(),
		Stock:     f.stock,
		Price:     domain.Price(decimal.RequireFromString("1010")),
		Quantity:  1,
		BuyOrder:  domain.Order{ID: ulid.Make(), Stock: f.stock},
		SellOrder: domain.Order{ID: ulid.Make(), Stock: f.stock},
		CreatedAt: time.Now().UTC(),
	}
	if err := f.hub.Publish(port.TradeExecutedEvent{Trade: trade}); err != nil {
		t.Fatalf("publish trade: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("received a trade after unsubscribing")
	}
}

func TestHubRejectsUnknownChannel(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	sendMessage(t, conn, clientCommand{Type: "subscribe", Channels: []string{"market.nope"}})

	message := readMessage(t, conn)
	if message.Type != "error" || !strings.Contains(message.Message, "unknown channel") {
		t.Fatalf("message = %+v, want unknown channel error", message)
	}
}

func TestHubRejectsUnknownStock(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	sendMessage(t, conn, clientCommand{
		Type:      "subscribe",
		Channels:  []string{ChannelMarketTicker},
		StockCode: "NONE",
	})

	message := readMessage(t, conn)
	if message.Type != "error" || message.StockCode != "NONE" || message.Message != "stock not found" {
		t.Fatalf("message = %+v, want stock not found error", message)
	}
}

func TestHubOrderUpdateRequiresUser(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	sendMessage(t, conn, clientCommand{
		Type:     "subscribe",
		Channels: []string{ChannelOrderUpdate},
	})

	message := readMessage(t, conn)
	if message.Type != "error" || !strings.Contains(message.Message, "userId is required") {
		t.Fatalf("message = %+v, want userId error", message)
	}
}

func TestHubAllStocksSubscriptionStreamsEverything(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	subscribe(t, conn, []string{ChannelMarketTrade}, "", "")

	other := domain.Stock{ID: ulid.Make(), Code: "BBCA"}
	for _, stock := range []domain.Stock{f.stock, other} {
		trade := domain.Trade{
			ID:        ulid.Make(),
			Stock:     stock,
			Price:     domain.Price(decimal.RequireFromString("1000")),
			Quantity:  1,
			BuyOrder:  domain.Order{ID: ulid.Make(), Stock: stock},
			SellOrder: domain.Order{ID: ulid.Make(), Stock: stock},
			CreatedAt: time.Now().UTC(),
		}
		if err := f.hub.Publish(port.TradeExecutedEvent{Trade: trade}); err != nil {
			t.Fatalf("publish trade: %v", err)
		}
	}

	for _, want := range []string{"BMRI", "BBCA"} {
		if message := readMessage(t, conn); message.StockCode != want {
			t.Fatalf("stock = %q, want %q", message.StockCode, want)
		}
	}
}

func TestHubCloseDisconnectsClients(t *testing.T) {
	f := newHubFixture(t)
	conn := f.dial(t, "")

	f.hub.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("read succeeded after hub close")
	}
}

func TestStockFilter(t *testing.T) {
	empty := parseStockFilter("")
	if !empty.allows("BMRI") || !empty.allows("BBCA") {
		t.Fatal("empty filter must allow every stock")
	}

	filter := parseStockFilter(" BMRI, BBCA ,BMRI, ")
	if len(filter.codes) != 2 {
		t.Fatalf("codes = %+v, want BMRI and BBCA", filter.codes)
	}
	if !filter.allows("BMRI") || !filter.allows("BBCA") {
		t.Fatal("filter must allow subscribed stocks")
	}
	if filter.allows("TLKM") {
		t.Fatal("filter must reject unknown stock")
	}
}

func newOrder(stock domain.Stock, userID string, status domain.OrderStatusType) domain.Order {
	now := time.Now().UTC()

	return domain.Order{
		ID:        ulid.Make(),
		UserID:    userID,
		Stock:     stock,
		Side:      domain.OrderSideBuy,
		Status:    status,
		Price:     decimal.RequireFromString("1000"),
		Qty:       100,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
