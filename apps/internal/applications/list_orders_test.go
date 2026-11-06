package applications

import (
	"errors"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/adapter/memdb"
	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/oklog/ulid/v2"
)

func TestListOrdersPaginationAndUserScope(t *testing.T) {
	repo := memdb.NewOrderRepo()
	uc := NewListOrders(repo)

	bmri := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	bbca := domain.Stock{ID: ulid.Make(), Code: "BBCA"}
	base := time.Now().UTC()

	first := makeOrder(bmri, "user-1", domain.OrderSideBuy, domain.OrderStatusNew, "1000", 10, base.Add(1*time.Minute))
	second := makeOrder(bmri, "user-1", domain.OrderSideBuy, domain.OrderStatusNew, "990", 20, base.Add(2*time.Minute))
	third := makeOrder(bbca, "user-1", domain.OrderSideBuy, domain.OrderStatusMatch, "500", 30, base.Add(3*time.Minute))
	other := makeOrder(bmri, "user-2", domain.OrderSideBuy, domain.OrderStatusNew, "1000", 40, base.Add(4*time.Minute))

	for _, order := range []domain.Order{first, second, third, other} {
		order := order
		if err := repo.Save(&order); err != nil {
			t.Fatalf("save order: %v", err)
		}
	}

	page, err := uc.Execute(ListOrdersQuery{UserID: "user-1", Limit: 2})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].ID != third.ID.String() || page.Data[1].ID != second.ID.String() {
		t.Fatalf("page 1 = %+v, want newest two orders", page.Data)
	}
	if page.NextPointer != second.ID.String() {
		t.Fatalf("next = %q, want %q", page.NextPointer, second.ID.String())
	}
	if page.Limit != 2 {
		t.Fatalf("limit = %d, want 2", page.Limit)
	}

	page, err = uc.Execute(ListOrdersQuery{UserID: "user-1", Limit: 2, Cursor: page.NextPointer})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != first.ID.String() {
		t.Fatalf("page 2 = %+v, want last order", page.Data)
	}
	if page.NextPointer != "" {
		t.Fatalf("page 2 next = %q, want empty", page.NextPointer)
	}

	page, err = uc.Execute(ListOrdersQuery{
		UserID:    "user-1",
		StockCode: "BMRI",
		Status:    domain.OrderStatusNew,
	})
	if err != nil {
		t.Fatalf("combined filter: %v", err)
	}
	if len(page.Data) != 2 || page.Data[0].ID != second.ID.String() || page.Data[1].ID != first.ID.String() {
		t.Fatalf("combined filter = %+v, want user-1 BMRI NEW orders", page.Data)
	}

	page, err = uc.Execute(ListOrdersQuery{UserID: "user-2"})
	if err != nil {
		t.Fatalf("user-2: %v", err)
	}
	if len(page.Data) != 1 || page.Data[0].ID != other.ID.String() {
		t.Fatalf("user-2 = %+v, want only its own order", page.Data)
	}
}

func TestListOrdersValidation(t *testing.T) {
	uc := NewListOrders(memdb.NewOrderRepo())

	tests := []struct {
		name  string
		query ListOrdersQuery
	}{
		{name: "invalid status", query: ListOrdersQuery{UserID: "user-1", Status: "BOGUS"}},
		{name: "invalid cursor", query: ListOrdersQuery{UserID: "user-1", Cursor: "not-a-ulid"}},
		{
			name:  "unknown cursor",
			query: ListOrdersQuery{UserID: "user-1", Cursor: ulid.Make().String()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := uc.Execute(tt.query); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestListOrdersWithoutUserReturnsAll(t *testing.T) {
	repo := memdb.NewOrderRepo()
	uc := NewListOrders(repo)

	stock := domain.Stock{ID: ulid.Make(), Code: "BMRI"}
	base := time.Now().UTC()
	for i, userID := range []string{"user-1", "user-2"} {
		order := makeOrder(stock, userID, domain.OrderSideBuy, domain.OrderStatusNew, "1000", i+1, base.Add(time.Duration(i)*time.Minute))
		if err := repo.Save(&order); err != nil {
			t.Fatalf("save order: %v", err)
		}
	}

	page, err := uc.Execute(ListOrdersQuery{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("orders = %d, want 2 from every user", len(page.Data))
	}
}

func TestListOrdersNormalizesLimit(t *testing.T) {
	uc := NewListOrders(memdb.NewOrderRepo())

	page, err := uc.Execute(ListOrdersQuery{UserID: "user-1"})
	if err != nil {
		t.Fatalf("default limit: %v", err)
	}
	if page.Limit != defaultLimit {
		t.Fatalf("limit = %d, want %d", page.Limit, defaultLimit)
	}

	page, err = uc.Execute(ListOrdersQuery{UserID: "user-1", Limit: maxLimit + 1})
	if err != nil {
		t.Fatalf("max limit: %v", err)
	}
	if page.Limit != maxLimit {
		t.Fatalf("limit = %d, want %d", page.Limit, maxLimit)
	}
}

func TestListOrdersReturnsEmptyPage(t *testing.T) {
	uc := NewListOrders(memdb.NewOrderRepo())

	page, err := uc.Execute(ListOrdersQuery{UserID: "user-1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(page.Data) != 0 {
		t.Fatalf("data = %+v, want empty", page.Data)
	}
	if page.NextPointer != "" {
		t.Fatalf("next = %q, want empty", page.NextPointer)
	}
}
