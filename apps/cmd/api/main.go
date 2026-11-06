package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	httpadapter "github.com/bowodev/trading-backend/apps/internal/adapter/http"
	"github.com/bowodev/trading-backend/apps/internal/adapter/matching_engine"
	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/adapter/ws"
	"github.com/bowodev/trading-backend/apps/internal/applications"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
)

func main() {
	stocks := memdb.NewStockRepo()
	orders := memdb.NewOrderRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	marketData := memdb.NewMarketDataRepo()

	stockCodes, err := seedStocks(stocks)
	if err != nil {
		log.Fatalf("seed stocks: %v", err)
	}

	engine := matching_engine.New(books)
	defer engine.Close()

	snapshots := applications.NewMarketSnapshot(stocks, books, trades, marketData)
	hub := ws.NewHub(snapshots, allowedOrigins())
	defer hub.Close()

	publisher := applications.NewTickerRecorder(marketData, fanoutPublisher{
		publishers: []port.IEventPublisher{logPublisher{}, hub},
	})

	simulator := applications.NewPriceSimulator(
		marketData,
		publisher,
		stockCodes,
		priceSimInterval(),
		priceSimVolatilityBps(),
	)
	if err := simulator.Seed(); err != nil {
		log.Fatalf("seed market data: %v", err)
	}
	if priceSimEnabled() {
		simulator.Start()
		log.Printf("price simulation enabled (interval=%s volatility=%dbps)", priceSimInterval(), priceSimVolatilityBps())
	}
	defer simulator.Close()

	handler := httpadapter.New(
		applications.NewPlaceOrder(stocks, orders, trades, engine, publisher),
		applications.NewListOrders(orders),
		applications.NewTradeHistory(trades),
		snapshots,
	)
	handler.Handle("GET /api/v1/ws", hub)

	addr := ":" + listenPort()
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdown

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("api listening on %s", addr)
	log.Printf("websocket endpoint ws://localhost%s/api/v1/ws", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

type logPublisher struct{}

func (logPublisher) Publish(event port.Event) error {
	// Skip tickers; they are high-volume and already visible via the snapshot API.
	if _, isTicker := event.(port.MarketTickerEvent); isTicker {
		return nil
	}

	log.Printf("event %s", event.EventName())
	return nil
}

type fanoutPublisher struct {
	publishers []port.IEventPublisher
}

func (p fanoutPublisher) Publish(event port.Event) error {
	for _, publisher := range p.publishers {
		if err := publisher.Publish(event); err != nil {
			return err
		}
	}

	return nil
}

func allowedOrigins() []string {
	raw := strings.TrimSpace(os.Getenv("WS_ALLOWED_ORIGINS"))
	if raw == "" {
		// Bruno and Postman send their own Origin; this build has no auth boundary,
		// so any origin is allowed unless the operator restricts it.
		return []string{"*"}
	}

	origins := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}

	return origins
}

func seedStocks(stocks port.IStockRepository) ([]string, error) {
	codes := []string{"BMRI", "BBCA", "BBRI", "TLKM"}
	if raw := strings.TrimSpace(os.Getenv("STOCK_CODES")); raw != "" {
		codes = strings.Split(raw, ",")
	}

	seeded := make([]string, 0, len(codes))
	for _, code := range codes {
		code = strings.ToUpper(strings.TrimSpace(code))
		if code == "" {
			continue
		}

		stock := domain.Stock{ID: ulid.Make(), Code: code}
		if err := stocks.Save(&stock); err != nil {
			return nil, err
		}
		seeded = append(seeded, code)
	}

	return seeded, nil
}

func priceSimEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("PRICE_SIM_ENABLED")))
	switch raw {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func priceSimInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PRICE_SIM_INTERVAL"))
	if raw == "" {
		return applications.DefaultPriceSimInterval
	}

	interval, err := time.ParseDuration(raw)
	if err != nil || interval <= 0 {
		log.Printf("invalid PRICE_SIM_INTERVAL %q, using %s", raw, applications.DefaultPriceSimInterval)
		return applications.DefaultPriceSimInterval
	}

	return interval
}

func priceSimVolatilityBps() int {
	raw := strings.TrimSpace(os.Getenv("PRICE_SIM_VOLATILITY_BPS"))
	if raw == "" {
		return applications.DefaultPriceVolatilityBps
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Printf("invalid PRICE_SIM_VOLATILITY_BPS %q, using %d", raw, applications.DefaultPriceVolatilityBps)
		return applications.DefaultPriceVolatilityBps
	}

	return value
}

func listenPort() string {
	if value := strings.TrimSpace(os.Getenv("PORT")); value != "" {
		return value
	}

	return "8080"
}
