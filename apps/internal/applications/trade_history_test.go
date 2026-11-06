package applications

import (
	"errors"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/oklog/ulid/v2"
)

func TestTradeHistoryPaginationAndUserScope(t *testing.T) {
	repo := memdb.NewTradeRepo()
	uc := NewTradeHistory(repo)

	stock := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	base := time.Now().UTC()
	buyOne := makeOrder(stock, "user-1", domain.OrderSideBuy, domain.OrderStatusMatch, "1000", 10, base)
	sellOne := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusMatch, "1000", 10, base)
	buyTwo := makeOrder(stock, "user-1", domain.OrderSideBuy, domain.OrderStatusMatch, "1010", 20, base)
	sellTwo := makeOrder(stock, "user-2", domain.OrderSideSell, domain.OrderStatusMatch, "1010", 20, base)
	otherBuy := makeOrder(stock, "user-3", domain.OrderSideBuy, domain.OrderStatusMatch, "1020", 30, base)
	otherSell := makeOrder(stock, "user-4", domain.OrderSideSell, domain.OrderStatusMatch, "1020", 30, base)

	first := makeTrade(stock, buyOne, sellOne, "1000", 10, base.Add(1*time.Minute))
	second := makeTrade(stock, buyTwo, sellTwo, "1010", 20, base.Add(2*time.Minute))
	other := makeTrade(stock, otherBuy, otherSell, "1020", 30, base.Add(3*time.Minute))

	for _, trade := range []domain.Trade{first, second, other} {
		trade := trade
		if err := repo.Save(&trade); err != nil {
			t.Fatalf("save trade: %v", err)
		}
	}

	page, err := uc.Execute(TradeHistoryQuery{UserID: "user-1", Limit: 1})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != second.ID.String() {
		t.Fatalf("page 1 = %+v, want newest user-1 trade", page.Data)
	}
	if page.NextPointer != second.ID.String() {
		t.Fatalf("next = %q, want %q", page.NextPointer, second.ID.String())
	}

	page, err = uc.Execute(TradeHistoryQuery{UserID: "user-1", Limit: 1, Cursor: page.NextPointer})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != first.ID.String() {
		t.Fatalf("page 2 = %+v, want older user-1 trade", page.Data)
	}
	if page.NextPointer != "" {
		t.Fatalf("page 2 next = %q, want empty", page.NextPointer)
	}

	page, err = uc.Execute(TradeHistoryQuery{UserID: "user-3"})
	if err != nil {
		t.Fatalf("user-3: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != other.ID.String() {
		t.Fatalf("user-3 = %+v, want only its own trade", page.Data)
	}
}

func TestTradeHistoryValidation(t *testing.T) {
	uc := NewTradeHistory(memdb.NewTradeRepo())

	tests := []struct {
		name  string
		query TradeHistoryQuery
	}{
		{name: "invalid cursor", query: TradeHistoryQuery{UserID: "user-1", Cursor: "not-a-ulid"}},
		{name: "unknown cursor", query: TradeHistoryQuery{UserID: "user-1", Cursor: ulid.Make().String()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := uc.Execute(tt.query); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestTradeHistoryWithoutUserReturnsAll(t *testing.T) {
	repo := memdb.NewTradeRepo()
	uc := NewTradeHistory(repo)

	stock := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	base := time.Now().UTC()
	for i, userID := range []string{"user-1", "user-2"} {
		buy := makeOrder(stock, userID, domain.OrderSideBuy, domain.OrderStatusMatch, "1000", 10, base)
		sell := makeOrder(stock, "counterparty", domain.OrderSideSell, domain.OrderStatusMatch, "1000", 10, base)
		trade := makeTrade(stock, buy, sell, "1000", 10, base.Add(time.Duration(i)*time.Minute))
		if err := repo.Save(&trade); err != nil {
			t.Fatalf("save trade: %v", err)
		}
	}

	page, err := uc.Execute(TradeHistoryQuery{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("trades = %d, want 2 from every user", len(page.Data))
	}
}
