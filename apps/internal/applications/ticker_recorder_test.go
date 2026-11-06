package applications

import (
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

func TestTickerRecorderUpdatesMarketDataAndPublishesTicker(t *testing.T) {
	marketData := memdb.NewMarketDataRepo()
	publisher := &fakePublisher{}
	recorder := NewTickerRecorder(marketData, publisher)

	stock := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	if err := marketData.Save(port.MarketData{
		StockCode: "BMRI",
		Last:      decimal.RequireFromString("1005"),
		PrevClose: decimal.RequireFromString("1000"),
		Volume:    1800,
	}); err != nil {
		t.Fatalf("save market data: %v", err)
	}

	trade := domain.Trade{
		ID:        ulid.Make(),
		Stock:     stock,
		Price:     domain.Price(decimal.RequireFromString("1010")),
		Quantity:  25,
		BuyOrder:  domain.Order{ID: ulid.Make(), Stock: stock},
		SellOrder: domain.Order{ID: ulid.Make(), Stock: stock},
		CreatedAt: time.Now().UTC(),
	}
	if err := recorder.Publish(port.TradeExecutedEvent{Trade: trade}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(publisher.events) != 2 {
		t.Fatalf("events = %d, want 2", len(publisher.events))
	}
	if _, ok := publisher.events[0].(port.TradeExecutedEvent); !ok {
		t.Fatalf("event[0] = %T, want TradeExecutedEvent", publisher.events[0])
	}
	ticker, ok := publisher.events[1].(port.MarketTickerEvent)
	if !ok {
		t.Fatalf("event[1] = %T, want MarketTickerEvent", publisher.events[1])
	}
	if ticker.Last.String() != "1010" || ticker.Change.String() != "10" || ticker.Volume != 1825 {
		t.Fatalf("ticker = %+v, want last=1010 change=10 volume=1825", ticker)
	}

	saved, err := marketData.Get("BMRI")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if saved.Last.String() != "1010" || saved.Volume != 1825 {
		t.Fatalf("market data = %+v, want last=1010 volume=1825", saved)
	}
}

func TestTickerRecorderBootstrapsMissingMarketData(t *testing.T) {
	marketData := memdb.NewMarketDataRepo()
	publisher := &fakePublisher{}
	recorder := NewTickerRecorder(marketData, publisher)

	stock := domain.Stock{ID: ulid.Make(), Code: "BBCA"}
	trade := domain.Trade{
		ID:        ulid.Make(),
		Stock:     stock,
		Price:     domain.Price(decimal.RequireFromString("500")),
		Quantity:  10,
		BuyOrder:  domain.Order{ID: ulid.Make(), Stock: stock},
		SellOrder: domain.Order{ID: ulid.Make(), Stock: stock},
		CreatedAt: time.Now().UTC(),
	}
	if err := recorder.Publish(port.TradeExecutedEvent{Trade: trade}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ticker, ok := publisher.events[1].(port.MarketTickerEvent)
	if !ok {
		t.Fatalf("event[1] = %T, want MarketTickerEvent", publisher.events[1])
	}
	if ticker.Last.String() != "500" || ticker.Change.String() != "0" || ticker.Volume != 10 {
		t.Fatalf("ticker = %+v, want last=500 change=0 volume=10", ticker)
	}
}
