package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/applications"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/coder/websocket"
)

const (
	sendBuffer   = 64
	writeTimeout = 5 * time.Second
	readLimit    = 64 << 10
)

var knownChannels = map[string]struct{}{
	ChannelMarketTicker:    {},
	ChannelMarketTrade:     {},
	ChannelMarketOrderBook: {},
	ChannelOrderUpdate:     {},
}

// Hub serves the realtime WebSocket endpoint. Clients start with no
// subscriptions and send subscribe/unsubscribe commands for named channels.
// Hub is also an IEventPublisher, so the same events that feed the REST flow
// can be streamed without extra plumbing.
type Hub struct {
	snapshots     *applications.MarketSnapshot
	acceptOptions *websocket.AcceptOptions

	mu      sync.RWMutex
	clients map[*client]struct{}
	closed  bool
	wg      sync.WaitGroup
}

func NewHub(snapshots *applications.MarketSnapshot, originPatterns []string) *Hub {
	return &Hub{
		snapshots: snapshots,
		acceptOptions: &websocket.AcceptOptions{
			OriginPatterns: originPatterns,
		},
		clients: make(map[*client]struct{}),
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, h.acceptOptions)
	if err != nil {
		// A rejected Origin is the usual cause of a failed desktop-client
		// handshake, so make it visible instead of returning silently.
		log.Printf("websocket accept: %v", err)
		return
	}
	conn.SetReadLimit(readLimit)

	c := &client{
		conn:          conn,
		send:          make(chan []byte, sendBuffer),
		done:          make(chan struct{}),
		subs:          make(map[string]*subscription),
		defaultUserID: strings.TrimSpace(r.URL.Query().Get("userId")),
	}

	if !h.add(c) {
		_ = conn.CloseNow()
		return
	}
	defer h.wg.Done()
	defer h.remove(c)

	go c.writeLoop()

	for {
		typ, payload, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			c.enqueue(errorMessage("text messages only"))
			continue
		}

		h.handleCommand(c, payload)
	}
}

func (h *Hub) handleCommand(c *client, payload []byte) {
	var cmd clientCommand
	if err := json.Unmarshal(payload, &cmd); err != nil {
		c.enqueue(errorMessage("invalid message: " + err.Error()))
		return
	}

	switch cmd.Type {
	case "subscribe":
		h.handleSubscribe(c, cmd)
	case "unsubscribe":
		h.handleUnsubscribe(c, cmd)
	case "ping":
		c.enqueue(Message{Type: "pong"})
	default:
		c.enqueue(errorMessage(fmt.Sprintf("unknown command %q", cmd.Type)))
	}
}

func (h *Hub) handleSubscribe(c *client, cmd clientCommand) {
	channels, err := normalizeChannels(cmd)
	if err != nil {
		c.enqueue(errorMessage(err.Error()))
		return
	}

	filter := parseStockFilter(cmd.StockCode)
	userID := strings.TrimSpace(cmd.UserID)
	if userID == "" {
		userID = c.defaultUserID
	}

	for _, channel := range channels {
		if channel == ChannelOrderUpdate && userID == "" {
			c.enqueue(errorMessage("userId is required for " + ChannelOrderUpdate))
			return
		}
	}

	// Fail fast on unknown stocks so a typo does not look like an idle stream.
	for _, code := range filter.codes {
		if _, err := h.snapshots.OrderBook(code); err != nil {
			message := "snapshot unavailable"
			if errors.Is(err, applications.ErrStockNotFound) {
				message = "stock not found"
			}
			c.enqueue(Message{Type: "error", StockCode: code, Message: message})
			return
		}
	}

	for _, channel := range channels {
		c.subscribe(channel, filter, userID)
	}

	c.enqueue(Message{
		Type: "subscribed",
		Data: subscriptionPayload{
			Channels:  channels,
			StockCode: strings.TrimSpace(cmd.StockCode),
			UserID:    userID,
		},
	})

	for _, channel := range channels {
		h.sendInitial(c, channel, filter)
	}
}

func (h *Hub) handleUnsubscribe(c *client, cmd clientCommand) {
	channels, err := normalizeChannels(cmd)
	if err != nil {
		c.enqueue(errorMessage(err.Error()))
		return
	}

	filter := parseStockFilter(cmd.StockCode)
	for _, channel := range channels {
		if err := c.unsubscribe(channel, filter); err != nil {
			c.enqueue(errorMessage(err.Error()))
			return
		}
	}

	c.enqueue(Message{
		Type: "unsubscribed",
		Data: subscriptionPayload{
			Channels:  channels,
			StockCode: strings.TrimSpace(cmd.StockCode),
		},
	})
}

func (h *Hub) sendInitial(c *client, channel string, filter stockFilter) {
	// An empty stock filter means "every stock"; there is no cheap way to
	// enumerate them, so those clients only receive live events.
	if filter.all {
		return
	}

	for _, code := range filter.codes {
		switch channel {
		case ChannelMarketTicker:
			view, err := h.snapshots.Execute(applications.MarketSnapshotQuery{
				StockCode:      code,
				ShowTradeLimit: 1,
			})
			if err != nil {
				continue
			}
			c.enqueue(newTickerMessage(code, view.Price))
		case ChannelMarketOrderBook:
			view, err := h.snapshots.OrderBook(code)
			if err != nil {
				continue
			}
			c.enqueue(newOrderBookMessage(code, view))
		}
	}
}

// Publish implements port.IEventPublisher. Unknown events are ignored.
func (h *Hub) Publish(event port.Event) error {
	switch e := event.(type) {
	case port.OrderPlacedEvent:
		if err := h.publishOrderUpdate(e.Order); err != nil {
			return err
		}
		// A matched order leaves no extra book change beyond the one the
		// trade events already broadcast.
		if e.Order.Status == domain.OrderStatusNew {
			h.publishOrderBook(e.Order.Stock.Code)
		}
	case port.OrderUpdatedEvent:
		return h.publishOrderUpdate(e.Order)
	case port.TradeExecutedEvent:
		if err := h.broadcast(newTradeMessage(e.Trade), e.Trade.Stock.Code, ""); err != nil {
			return err
		}
		h.publishOrderBook(e.Trade.Stock.Code)
	case port.MarketTickerEvent:
		return h.broadcast(newMarketTickerMessage(e), e.StockCode, "")
	}

	return nil
}

func (h *Hub) publishOrderUpdate(order domain.Order) error {
	return h.broadcast(newOrderUpdateMessage(order), order.Stock.Code, order.UserID)
}

// publishOrderBook refreshes the aggregated depth after any change to the
// book. The book was already committed before the event was published.
func (h *Hub) publishOrderBook(stockCode string) {
	view, err := h.snapshots.OrderBook(stockCode)
	if err != nil {
		return
	}

	_ = h.broadcast(newOrderBookMessage(stockCode, view), stockCode, "")
}

func (h *Hub) broadcast(message Message, stockCode, userID string) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}

	var slow []*client

	h.mu.RLock()
	for c := range h.clients {
		if !c.wants(message.Type, stockCode, userID) {
			continue
		}

		select {
		case c.send <- payload:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	// Detach outside the publisher path so a stuck client cannot delay an
	// order placement. Removal is synchronous so a burst of events cannot
	// spawn an unbounded number of cleanup goroutines.
	for _, c := range slow {
		h.remove(c)
	}

	return nil
}

// Close disconnects every client. The HTTP server does not own hijacked
// WebSocket connections, so this must be called during shutdown.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true

	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = make(map[*client]struct{})
	h.mu.Unlock()

	for _, c := range clients {
		c.close()
	}

	// Every connection handler owns a read loop and a write loop. Waiting
	// here makes shutdown deterministic and proves no client goroutine leaks.
	h.wg.Wait()
}

func (h *Hub) add(c *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return false
	}

	h.clients[c] = struct{}{}
	h.wg.Add(1)
	return true
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	_, found := h.clients[c]
	if found {
		delete(h.clients, c)
	}
	h.mu.Unlock()

	if found {
		c.close()
	}
}

type client struct {
	conn          *websocket.Conn
	defaultUserID string

	send chan []byte
	done chan struct{}

	mu   sync.RWMutex
	subs map[string]*subscription

	closeOnce sync.Once
}

type subscription struct {
	all    bool
	codes  map[string]struct{}
	userID string
}

func (c *client) subscribe(channel string, filter stockFilter, userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sub, found := c.subs[channel]
	if !found {
		sub = &subscription{codes: make(map[string]struct{})}
		c.subs[channel] = sub
	}

	if filter.all || sub.all {
		sub.all = true
		sub.codes = make(map[string]struct{})
	} else {
		for _, code := range filter.codes {
			sub.codes[code] = struct{}{}
		}
	}

	if userID != "" {
		sub.userID = userID
	}
}

func (c *client) unsubscribe(channel string, filter stockFilter) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	sub, found := c.subs[channel]
	if !found {
		return nil
	}

	if filter.all {
		delete(c.subs, channel)
		return nil
	}

	if sub.all {
		return fmt.Errorf("unsubscribe from all stocks by omitting stockCode")
	}

	for _, code := range filter.codes {
		delete(sub.codes, code)
	}
	if len(sub.codes) == 0 {
		delete(c.subs, channel)
	}

	return nil
}

// wants reports whether this client asked for the given channel and stock.
// Order updates are additionally scoped to the client's user.
func (c *client) wants(channel, stockCode, userID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sub, found := c.subs[channel]
	if !found {
		return false
	}

	if sub.userID != "" && userID != "" && sub.userID != userID {
		return false
	}
	if sub.all {
		return true
	}

	_, found = sub.codes[stockCode]
	return found
}

func (c *client) enqueue(message Message) {
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}

	select {
	case c.send <- payload:
	default:
		// Slow client: drop the frame instead of blocking the reader.
	}
}

func (c *client) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case payload := <-c.send:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.conn.Write(ctx, websocket.MessageText, payload)
			cancel()

			if err != nil {
				c.close()
				return
			}
		}
	}
}

// close skips the close handshake: a stuck peer must never block the event
// publisher or server shutdown.
func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.CloseNow()
	})
}

func errorMessage(text string) Message {
	return Message{Type: "error", Message: text}
}

func normalizeChannels(cmd clientCommand) ([]string, error) {
	raw := make([]string, 0, len(cmd.Channels)+1)
	if cmd.Channel != "" {
		raw = append(raw, cmd.Channel)
	}
	raw = append(raw, cmd.Channels...)

	seen := make(map[string]struct{}, len(raw))
	channels := make([]string, 0, len(raw))

	for _, channel := range raw {
		channel = strings.TrimSpace(channel)
		if channel == "" {
			continue
		}
		if _, found := knownChannels[channel]; !found {
			return nil, fmt.Errorf("unknown channel %q", channel)
		}
		if _, duplicate := seen[channel]; duplicate {
			continue
		}

		seen[channel] = struct{}{}
		channels = append(channels, channel)
	}

	if len(channels) == 0 {
		return nil, errors.New("at least one channel is required")
	}

	return channels, nil
}

type stockFilter struct {
	all   bool
	codes []string
	set   map[string]struct{}
}

func parseStockFilter(raw string) stockFilter {
	filter := stockFilter{set: make(map[string]struct{})}

	for _, part := range strings.Split(raw, ",") {
		code := strings.TrimSpace(part)
		if code == "" {
			continue
		}
		if _, found := filter.set[code]; found {
			continue
		}

		filter.codes = append(filter.codes, code)
		filter.set[code] = struct{}{}
	}

	filter.all = len(filter.codes) == 0
	return filter
}

// allows reports whether an event for stockCode should reach a filter. An
// empty filter subscribes to every stock.
func (f stockFilter) allows(stockCode string) bool {
	if f.all {
		return true
	}

	_, found := f.set[stockCode]
	return found
}
