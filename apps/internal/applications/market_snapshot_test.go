package applications

import (
	"errors"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

func TestMarketSnapshot(t *testing.T) {
	stocks := memdb.NewStockRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	marketData := memdb.NewMarketDataRepo()
	uc := NewMarketSnapshot(stocks, books, trades, marketData)

	stock := seedStock(t, stocks, "BMRI")
	base := time.Now().UTC()

	if err := marketData.Save(port.MarketData{
		StockCode: "BMRI",
		Last:      decimal.RequireFromString("1005"),
		PrevClose: decimal.RequireFromString("1000"),
		Volume:    1800,
		UpdatedAt: base,
	}); err != nil {
		t.Fatalf("save market data: %v", err)
	}

	bookOrders := []domain.Order{
		makeOrder(stock, "user-2", domain.OrderSideBuy, domain.OrderStatusNew, "999", 200, base),
		makeOrder(stock, "user-3", domain.OrderSideBuy, domain.OrderStatusNew, "999", 300, base),
		makeOrder(stock, "user-4", domain.OrderSideBuy, domain.OrderStatusNew, "998", 1200, base),
		makeOrder(stock, "user-5", domain.OrderSideSell, domain.OrderStatusNew, "1000", 300, base),
		makeOrder(stock, "user-6", domain.OrderSideSell, domain.OrderStatusNew, "1001", 800, base),
	}
	for i := range bookOrders {
		if err := books.Add(&bookOrders[i]); err != nil {
			t.Fatalf("add book order: %v", err)
		}
	}

	buyOne := makeOrder(stock, "user-1", domain.OrderSideBuy, domain.OrderStatusMatch, "1000", 10, base)
	sellOne := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusMatch, "1000", 10, base)
	buyTwo := makeOrder(stock, "user-1", domain.OrderSideBuy, domain.OrderStatusMatch, "1004", 5, base)
	sellTwo := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusMatch, "1004", 5, base)
	for _, trade := range []domain.Trade{
		makeTrade(stock, buyOne, sellOne, "1000", 10, base.Add(1*time.Minute)),
		makeTrade(stock, buyTwo, sellTwo, "1004", 5, base.Add(2*time.Minute)),
	} {
		trade := trade
		if err := trades.Save(&trade); err != nil {
			t.Fatalf("save trade: %v", err)
		}
	}

	snapshot, err := uc.Execute(MarketSnapshotQuery{StockCode: "BMRI", ShowTradeLimit: 1})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if snapshot.StockCode != "BMRI" {
		t.Fatalf("stock code = %s, want BMRI", snapshot.StockCode)
	}
	if snapshot.Price.Last != "1005" || snapshot.Price.Change != "5" || snapshot.Price.Volume != 1800 {
		t.Fatalf("price = %+v, want last=1005 change=5 volume=1800", snapshot.Price)
	}

	wantBids := []OrderBookLevelView{{Price: "999", Qty: 500}, {Price: "998", Qty: 1200}}
	if len(snapshot.OrderBook.Bids) != len(wantBids) {
		t.Fatalf("bids = %+v, want %+v", snapshot.OrderBook.Bids, wantBids)
	}
	for i := range wantBids {
		if snapshot.OrderBook.Bids[i] != wantBids[i] {
			t.Fatalf("bids[%d] = %+v, want %+v", i, snapshot.OrderBook.Bids[i], wantBids[i])
		}
	}

	wantAsks := []OrderBookLevelView{{Price: "1000", Qty: 300}, {Price: "1001", Qty: 800}}
	if len(snapshot.OrderBook.Asks) != len(wantAsks) {
		t.Fatalf("asks = %+v, want %+v", snapshot.OrderBook.Asks, wantAsks)
	}
	for i := range wantAsks {
		if snapshot.OrderBook.Asks[i] != wantAsks[i] {
			t.Fatalf("asks[%d] = %+v, want %+v", i, snapshot.OrderBook.Asks[i], wantAsks[i])
		}
	}

	if len(snapshot.Trades) != 1 || snapshot.Trades[0].Price != "1004" {
		t.Fatalf("trades = %+v, want newest trade only", snapshot.Trades)
	}
}

func TestMarketSnapshotFallbacks(t *testing.T) {
	stocks := memdb.NewStockRepo()
	books := memdb.NewOrderBookRepo()
	trades := memdb.NewTradeRepo()
	marketData := memdb.NewMarketDataRepo()
	uc := NewMarketSnapshot(stocks, books, trades, marketData)

	stock := seedStock(t, stocks, "BMRI")
	base := time.Now().UTC()
	buy := makeOrder(stock, "user-1", domain.OrderSideBuy, domain.OrderStatusMatch, "1000", 10, base)
	sell := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusMatch, "1000", 10, base)
	trade := makeTrade(stock, buy, sell, "1000", 10, base)
	if err := trades.Save(&trade); err != nil {
		t.Fatalf("save trade: %v", err)
	}

	snapshot, err := uc.Execute(MarketSnapshotQuery{StockCode: "BMRI"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if snapshot.Price.Last != "1000" {
		t.Fatalf("last = %s, want 1000 from latest trade", snapshot.Price.Last)
	}
	if snapshot.Price.Change != "0" {
		t.Fatalf("change = %s, want 0", snapshot.Price.Change)
	}
	if snapshot.Price.Volume != 0 {
		t.Fatalf("volume = %d, want 0", snapshot.Price.Volume)
	}
	if len(snapshot.OrderBook.Bids) != 0 || len(snapshot.OrderBook.Asks) != 0 {
		t.Fatalf("book = %+v, want empty", snapshot.OrderBook)
	}
}

func TestMarketSnapshotValidation(t *testing.T) {
	stocks := memdb.NewStockRepo()
	uc := NewMarketSnapshot(stocks, memdb.NewOrderBookRepo(), memdb.NewTradeRepo(), memdb.NewMarketDataRepo())

	if _, err := uc.Execute(MarketSnapshotQuery{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty stock err = %v, want ErrInvalidArgument", err)
	}
	if _, err := uc.Execute(MarketSnapshotQuery{StockCode: "BMRI"}); !errors.Is(err, ErrStockNotFound) {
		t.Fatalf("missing stock err = %v, want ErrStockNotFound", err)
	}

	seedStock(t, stocks, "BMRI")
	snapshot, err := uc.Execute(MarketSnapshotQuery{StockCode: "BMRI", ShowTradeLimit: maxLimit + 1})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if snapshot.Price.Last != "0" {
		t.Fatalf("last = %s, want 0 when no trade", snapshot.Price.Last)
	}
}
