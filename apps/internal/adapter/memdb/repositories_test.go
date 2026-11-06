package memdb

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"
)

func newStock(code string) domain.Stock {
	return domain.Stock{ID: ulid.Make(), Code: code}
}

func newOrder(stock domain.Stock, side domain.OrderSideType, price string, qty int, createdAt time.Time) domain.Order {
	return domain.Order{
		ID:        ulid.Make(),
		UserID:    "user-1",
		Stock:     stock,
		Side:      side,
		Status:    domain.OrderStatusNew,
		Price:     decimal.RequireFromString(price),
		Qty:       qty,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

func TestStockRepo(t *testing.T) {
	repo := NewStockRepo()
	stock := newStock("BMRI")

	if err := repo.Save(&stock); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindByCode("BMRI")
	if err != nil {
		t.Fatalf("FindByCode: %v", err)
	}
	if got.ID != stock.ID {
		t.Fatalf("FindByCode ID = %s, want %s", got.ID, stock.ID)
	}

	updated := stock
	updated.Code = "BMRI"
	updated.ID = ulid.Make()
	if err := repo.Save(&updated); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, err = repo.FindByCode("BMRI")
	if err != nil {
		t.Fatalf("FindByCode after update: %v", err)
	}
	if got.ID != updated.ID {
		t.Fatalf("FindByCode after update ID = %s, want %s", got.ID, updated.ID)
	}

	if _, err := repo.FindByCode("BBCA"); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("FindByCode missing err = %v, want ErrDataNotFound", err)
	}
	if err := repo.Save(nil); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("Save nil err = %v, want ErrInvalidData", err)
	}
	if err := repo.Save(&domain.Stock{}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("Save empty code err = %v, want ErrInvalidData", err)
	}
}

func TestOrderRepo(t *testing.T) {
	repo := NewOrderRepo()
	stock := newStock("BMRI")
	otherStock := newStock("BBCA")
	base := time.Now()

	orders := []domain.Order{
		newOrder(stock, domain.OrderSideBuy, "1000", 10, base.Add(1*time.Minute)),
		newOrder(stock, domain.OrderSideSell, "1010", 20, base.Add(2*time.Minute)),
		newOrder(stock, domain.OrderSideBuy, "990", 30, base.Add(3*time.Minute)),
		newOrder(otherStock, domain.OrderSideBuy, "500", 40, base.Add(4*time.Minute)),
	}
	for i := range orders {
		if err := repo.Save(&orders[i]); err != nil {
			t.Fatalf("Save order %d: %v", i, err)
		}
	}

	got, err := repo.FindByID(orders[0].ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Qty != orders[0].Qty {
		t.Fatalf("FindByID Qty = %d, want %d", got.Qty, orders[0].Qty)
	}

	page, next, err := repo.List(port.OrderFilter{StockCode: "BMRI"}, ulid.ULID{}, 2)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("List page 1 len = %d, want 2", len(page))
	}
	if page[0].ID != orders[2].ID || page[1].ID != orders[1].ID {
		t.Fatalf("List page 1 not sorted newest first")
	}
	if next != orders[1].ID {
		t.Fatalf("List page 1 next = %s, want %s", next, orders[1].ID)
	}

	page, next, err = repo.List(port.OrderFilter{StockCode: "BMRI"}, next, 2)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page) != 1 || page[0].ID != orders[0].ID {
		t.Fatalf("List page 2 = %+v, want last order", page)
	}
	if next != (ulid.ULID{}) {
		t.Fatalf("List page 2 next = %s, want zero ULID", next)
	}

	page, _, err = repo.List(port.OrderFilter{Status: "NEW"}, ulid.ULID{}, 10)
	if err != nil {
		t.Fatalf("List by status: %v", err)
	}
	if len(page) != len(orders) {
		t.Fatalf("List by status len = %d, want %d", len(page), len(orders))
	}

	page, _, err = repo.List(port.OrderFilter{StockCode: "BMRI", Status: "NEW"}, ulid.ULID{}, 10)
	if err != nil {
		t.Fatalf("List combined filter: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("List combined filter len = %d, want 3 BMRI NEW orders", len(page))
	}

	if _, _, err := repo.List(port.OrderFilter{Status: "UNKNOWN"}, ulid.ULID{}, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("List invalid status err = %v, want ErrInvalidArgument", err)
	}
	if _, _, err := repo.List(port.OrderFilter{}, ulid.Make(), 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("List unknown cursor err = %v, want ErrInvalidArgument", err)
	}

	orders[0].Status = domain.OrderStatusMatch
	if err := repo.Update(&orders[0]); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = repo.FindByID(orders[0].ID)
	if err != nil {
		t.Fatalf("FindByID after update: %v", err)
	}
	if got.Status != domain.OrderStatusMatch {
		t.Fatalf("Update status = %s, want MATCHED", got.Status)
	}

	missing := newOrder(stock, domain.OrderSideBuy, "1000", 1, time.Now())
	if err := repo.Update(&missing); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("Update missing err = %v, want ErrDataNotFound", err)
	}
	if err := repo.Save(nil); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("Save nil err = %v, want ErrInvalidData", err)
	}
}

func TestTradeRepo(t *testing.T) {
	repo := NewTradeRepo()
	stock := newStock("BMRI")
	base := time.Now()

	trades := []domain.Trade{
		{
			ID:        ulid.Make(),
			Stock:     stock,
			Price:     domain.Price(decimal.RequireFromString("1000")),
			Quantity:  10,
			BuyOrder:  newOrder(stock, domain.OrderSideBuy, "1000", 10, base),
			SellOrder: newOrder(stock, domain.OrderSideSell, "1000", 10, base),
			CreatedAt: base.Add(1 * time.Minute),
		},
		{
			ID:        ulid.Make(),
			Stock:     stock,
			Price:     domain.Price(decimal.RequireFromString("1010")),
			Quantity:  20,
			BuyOrder:  newOrder(stock, domain.OrderSideBuy, "1010", 20, base),
			SellOrder: newOrder(stock, domain.OrderSideSell, "1010", 20, base),
			CreatedAt: base.Add(2 * time.Minute),
		},
		{
			ID:        ulid.Make(),
			Stock:     newStock("BBCA"),
			Price:     domain.Price(decimal.RequireFromString("500")),
			Quantity:  30,
			BuyOrder:  newOrder(newStock("BBCA"), domain.OrderSideBuy, "500", 30, base),
			SellOrder: newOrder(newStock("BBCA"), domain.OrderSideSell, "500", 30, base),
			CreatedAt: base.Add(3 * time.Minute),
		},
	}
	for i := range trades {
		if err := repo.Save(&trades[i]); err != nil {
			t.Fatalf("Save trade %d: %v", i, err)
		}
	}

	got, err := repo.ListByStock("BMRI", 10)
	if err != nil {
		t.Fatalf("ListByStock: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByStock len = %d, want 2", len(got))
	}
	if got[0].ID != trades[1].ID || got[1].ID != trades[0].ID {
		t.Fatalf("ListByStock not sorted newest first")
	}

	got, err = repo.ListByStock("BMRI", 1)
	if err != nil {
		t.Fatalf("ListByStock limit: %v", err)
	}
	if len(got) != 1 || got[0].ID != trades[1].ID {
		t.Fatalf("ListByStock limit = %+v, want newest trade only", got)
	}

	page, next, err := repo.List("stockCode", "BMRI", ulid.ULID{}, 1)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(page) != 1 || page[0].ID != trades[1].ID {
		t.Fatalf("List page 1 = %+v, want newest trade", page)
	}
	if next != trades[1].ID {
		t.Fatalf("List page 1 next = %s, want %s", next, trades[1].ID)
	}

	page, next, err = repo.List("stockCode", "BMRI", next, 1)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(page) != 1 || page[0].ID != trades[0].ID {
		t.Fatalf("List page 2 = %+v, want second trade", page)
	}
	if next != (ulid.ULID{}) {
		t.Fatalf("List page 2 next = %s, want zero ULID", next)
	}

	page, _, err = repo.List("buyOrderId", trades[0].BuyOrder.ID.String(), ulid.ULID{}, 10)
	if err != nil {
		t.Fatalf("List by buy order: %v", err)
	}
	if len(page) != 1 || page[0].ID != trades[0].ID {
		t.Fatalf("List by buy order = %+v, want first trade", page)
	}

	if _, _, err := repo.List("unknown", "", ulid.ULID{}, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("List invalid filter err = %v, want ErrInvalidArgument", err)
	}
	if _, _, err := repo.List("", "", ulid.Make(), 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("List unknown cursor err = %v, want ErrInvalidArgument", err)
	}
}

func TestOrderBookRepo(t *testing.T) {
	repo := NewOrderBookRepo()
	stock := newStock("BMRI")
	base := time.Now()

	bids := []domain.Order{
		newOrder(stock, domain.OrderSideBuy, "100", 10, base),
		newOrder(stock, domain.OrderSideBuy, "102", 20, base),
		newOrder(stock, domain.OrderSideBuy, "101", 30, base),
	}
	asks := []domain.Order{
		newOrder(stock, domain.OrderSideSell, "105", 10, base),
		newOrder(stock, domain.OrderSideSell, "103", 20, base),
		newOrder(stock, domain.OrderSideSell, "104", 30, base),
	}

	for i := range bids {
		if err := repo.Add(&bids[i]); err != nil {
			t.Fatalf("Add bid %d: %v", i, err)
		}
	}
	for i := range asks {
		if err := repo.Add(&asks[i]); err != nil {
			t.Fatalf("Add ask %d: %v", i, err)
		}
	}

	bestBids, err := repo.BestBids(stock.ID, 10)
	if err != nil {
		t.Fatalf("BestBids: %v", err)
	}
	if len(bestBids) != 3 {
		t.Fatalf("BestBids len = %d, want 3", len(bestBids))
	}
	if bestBids[0].Price.String() != "102" || bestBids[1].Price.String() != "101" || bestBids[2].Price.String() != "100" {
		t.Fatalf("BestBids order = %s, %s, %s", bestBids[0].Price, bestBids[1].Price, bestBids[2].Price)
	}

	bestAsks, err := repo.BestAsks(stock.ID, 10)
	if err != nil {
		t.Fatalf("BestAsks: %v", err)
	}
	if len(bestAsks) != 3 {
		t.Fatalf("BestAsks len = %d, want 3", len(bestAsks))
	}
	if bestAsks[0].Price.String() != "103" || bestAsks[1].Price.String() != "104" || bestAsks[2].Price.String() != "105" {
		t.Fatalf("BestAsks order = %s, %s, %s", bestAsks[0].Price, bestAsks[1].Price, bestAsks[2].Price)
	}

	limited, err := repo.BestBids(stock.ID, 2)
	if err != nil {
		t.Fatalf("BestBids limit: %v", err)
	}
	if len(limited) != 2 || limited[0].Price.String() != "102" || limited[1].Price.String() != "101" {
		t.Fatalf("BestBids limit = %+v, want top 2", limited)
	}

	empty, err := repo.BestAsks(stock.ID, 0)
	if err != nil {
		t.Fatalf("BestAsks zero limit: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("BestAsks zero limit len = %d, want 0", len(empty))
	}

	if err := repo.Add(&domain.Order{Stock: stock, Side: "INVALID"}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("Add invalid side err = %v, want ErrInvalidData", err)
	}

	book, err := repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	book.Bids["999"] = append(book.Bids["999"], bids[0])
	delete(book.Asks, asks[0].Price.String())
	book.Bids[bids[0].Price.String()][0].Qty = 999

	fresh, err := repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get fresh: %v", err)
	}
	if _, found := fresh.Bids["999"]; found {
		t.Fatalf("Get returned internal map, mutation leaked")
	}
	if _, found := fresh.Asks[asks[0].Price.String()]; !found {
		t.Fatalf("Get returned internal map, deletion leaked")
	}
	if fresh.Bids[bids[0].Price.String()][0].Qty != 10 {
		t.Fatalf("Get returned internal slice, mutation leaked")
	}

	updated := bids[0]
	updated.Side = domain.OrderSideSell
	updated.Price = decimal.RequireFromString("103")
	updated.Qty = 50
	if err := repo.Update(&updated); err != nil {
		t.Fatalf("Update: %v", err)
	}
	book, err = repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	for _, order := range book.Bids["100"] {
		if order.ID == updated.ID {
			t.Fatalf("updated order still in old price level")
		}
	}
	found := false
	for _, order := range book.Asks["103"] {
		if order.ID == updated.ID && order.Qty == 50 {
			found = true
		}
	}
	if !found {
		t.Fatalf("updated order not moved to new side/price level")
	}

	if err := repo.Remove(stock.ID, asks[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := repo.Remove(stock.ID, asks[0].ID); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("Remove missing err = %v, want ErrDataNotFound", err)
	}

	if err := repo.Update(&domain.Order{ID: ulid.Make(), Stock: stock, Side: domain.OrderSideBuy}); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("Update missing err = %v, want ErrDataNotFound", err)
	}
}

func TestOrderBookRepoApply(t *testing.T) {
	repo := NewOrderBookRepo()
	stock := newStock("BMRI")
	base := time.Now()

	resting := newOrder(stock, domain.OrderSideSell, "100", 10, base)
	removed := newOrder(stock, domain.OrderSideSell, "101", 5, base.Add(time.Second))
	for _, order := range []*domain.Order{&resting, &removed} {
		if err := repo.Add(order); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	updated := resting
	updated.Qty = 6
	incoming := newOrder(stock, domain.OrderSideBuy, "100", 4, base.Add(2*time.Second))

	err := repo.Apply(stock, port.OrderBookChange{
		Upserts: []domain.Order{updated, incoming},
		Removes: []ulid.ULID{removed.ID},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	book, err := repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(book.Asks["100"]) != 1 || book.Asks["100"][0].Qty != 6 {
		t.Fatalf("asks[100] = %+v, want single qty 6", book.Asks["100"])
	}
	if _, found := book.Asks["101"]; found {
		t.Fatalf("asks[101] = %+v, want removed", book.Asks["101"])
	}
	if len(book.Bids["100"]) != 1 || book.Bids["100"][0].Qty != 4 {
		t.Fatalf("bids[100] = %+v, want single qty 4", book.Bids["100"])
	}

	// A failing remove must leave the previous book untouched.
	err = repo.Apply(stock, port.OrderBookChange{
		Upserts: []domain.Order{incoming},
		Removes: []ulid.ULID{ulid.Make()},
	})
	if !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("Apply failing remove err = %v, want ErrDataNotFound", err)
	}

	book, err = repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get after failure: %v", err)
	}
	if len(book.Asks["100"]) != 1 || book.Asks["100"][0].Qty != 6 {
		t.Fatalf("asks[100] = %+v, want unchanged qty 6", book.Asks["100"])
	}
	if len(book.Bids["100"]) != 1 || book.Bids["100"][0].ID != incoming.ID {
		t.Fatalf("bids[100] = %+v, want unchanged incoming bid", book.Bids["100"])
	}
}

func TestOrderBookRepoApplyCreatesBook(t *testing.T) {
	repo := NewOrderBookRepo()
	stock := newStock("BMRI")
	order := newOrder(stock, domain.OrderSideBuy, "100", 10, time.Now())

	if err := repo.Apply(stock, port.OrderBookChange{Upserts: []domain.Order{order}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	book, err := repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(book.Bids["100"]) != 1 || book.Bids["100"][0].ID != order.ID {
		t.Fatalf("bids[100] = %+v, want applied order", book.Bids["100"])
	}

	other := newStock("BBCA")
	foreign := newOrder(other, domain.OrderSideBuy, "500", 1, time.Now())
	if err := repo.Apply(stock, port.OrderBookChange{Upserts: []domain.Order{foreign}}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("Apply foreign stock err = %v, want ErrInvalidData", err)
	}
}

func TestOrderBookRepoSnapshot(t *testing.T) {
	repo := NewOrderBookRepo()
	stock := newStock("BMRI")
	base := time.Now()

	orders := []domain.Order{
		newOrder(stock, domain.OrderSideBuy, "100", 10, base),
		newOrder(stock, domain.OrderSideBuy, "102", 20, base),
		newOrder(stock, domain.OrderSideBuy, "101", 30, base),
		newOrder(stock, domain.OrderSideSell, "105", 10, base),
		newOrder(stock, domain.OrderSideSell, "103", 20, base),
		newOrder(stock, domain.OrderSideSell, "104", 30, base),
	}
	for i := range orders {
		if err := repo.Add(&orders[i]); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	bids, asks, err := repo.Snapshot(stock.ID, 2)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(bids) != 2 || bids[0].Price.String() != "102" || bids[1].Price.String() != "101" {
		t.Fatalf("bids = %+v, want top 2 descending", bids)
	}
	if len(asks) != 2 || asks[0].Price.String() != "103" || asks[1].Price.String() != "104" {
		t.Fatalf("asks = %+v, want top 2 ascending", asks)
	}

	bids[0].Qty = 999
	freshBids, _, err := repo.Snapshot(stock.ID, 2)
	if err != nil {
		t.Fatalf("Snapshot fresh: %v", err)
	}
	if freshBids[0].Qty == 999 {
		t.Fatalf("Snapshot returned shared order storage")
	}

	if _, _, err := repo.Snapshot(ulid.Make(), 2); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("Snapshot missing err = %v, want ErrDataNotFound", err)
	}
}

func TestOrderBookRepoConcurrentGet(t *testing.T) {
	repo := NewOrderBookRepo()
	stock := newStock("BMRI")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		order := newOrder(stock, domain.OrderSideBuy, "100", i+1, time.Now())
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := repo.Add(&order); err != nil {
				t.Errorf("Add: %v", err)
			}
			_, _ = repo.Get(stock.ID)
		}()
	}
	wg.Wait()

	book, err := repo.Get(stock.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := len(book.Bids["100"]); got != 50 {
		t.Fatalf("book len = %d, want 50", got)
	}
}
