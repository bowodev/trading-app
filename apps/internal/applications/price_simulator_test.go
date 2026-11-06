package applications

import (
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

func TestPriceSimulatorSeedsAndPublishesTicker(t *testing.T) {
	marketData := memdb.NewMarketDataRepo()
	publisher := &fakePublisher{}
	simulator := NewPriceSimulator(marketData, publisher, []string{"bmri", "BMRI", ""}, time.Hour, 50)

	if err := simulator.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	seeded, err := marketData.Get("BMRI")
	if err != nil {
		t.Fatalf("Get seeded: %v", err)
	}
	if seeded.Last.String() != "6000" || seeded.PrevClose.String() != "6000" {
		t.Fatalf("seeded = %+v, want last=prevClose=6000", seeded)
	}

	simulator.step()

	moved, err := marketData.Get("BMRI")
	if err != nil {
		t.Fatalf("Get after step: %v", err)
	}
	if moved.Last.Equal(seeded.Last) {
		t.Fatalf("last price did not move: %s", moved.Last)
	}
	if moved.Volume != seeded.Volume || !moved.PrevClose.Equal(seeded.PrevClose) {
		t.Fatalf("step changed volume/prev close: %+v", moved)
	}

	if len(publisher.events) != 1 {
		t.Fatalf("events = %d, want 1 ticker", len(publisher.events))
	}
	ticker, ok := publisher.events[0].(port.MarketTickerEvent)
	if !ok {
		t.Fatalf("event = %T, want MarketTickerEvent", publisher.events[0])
	}
	if ticker.StockCode != "BMRI" || !ticker.Last.Equal(moved.Last) {
		t.Fatalf("ticker = %+v, want BMRI at %s", ticker, moved.Last)
	}
	if !ticker.Change.Equal(moved.Last.Sub(moved.PrevClose)) {
		t.Fatalf("change = %s, want %s", ticker.Change, moved.Last.Sub(moved.PrevClose))
	}
}

func TestPriceSimulatorSeedRepairsMissingPrevClose(t *testing.T) {
	marketData := memdb.NewMarketDataRepo()
	if err := marketData.Save(port.MarketData{
		StockCode: "BBCA",
		Last:      decimal.RequireFromString("9100"),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	simulator := NewPriceSimulator(marketData, &fakePublisher{}, []string{"BBCA"}, time.Hour, 50)
	if err := simulator.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	data, err := marketData.Get("BBCA")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if data.Last.String() != "9100" || data.PrevClose.String() != "9100" {
		t.Fatalf("data = %+v, want prev close copied from last", data)
	}
}

func TestPriceSimulatorStartAndClose(t *testing.T) {
	marketData := memdb.NewMarketDataRepo()
	publisher := &channelPublisher{events: make(chan port.Event, 4)}
	simulator := NewPriceSimulator(marketData, publisher, []string{"BMRI"}, 5*time.Millisecond, 50)
	if err := simulator.Seed(); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	simulator.Start()

	select {
	case event := <-publisher.events:
		if _, ok := event.(port.MarketTickerEvent); !ok {
			t.Fatalf("event = %T, want MarketTickerEvent", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("simulator did not publish a ticker")
	}

	simulator.Close()
}

type channelPublisher struct {
	events chan port.Event
}

func (p *channelPublisher) Publish(event port.Event) error {
	select {
	case p.events <- event:
	default:
	}

	return nil
}
