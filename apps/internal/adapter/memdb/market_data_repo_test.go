package memdb

import (
	"errors"
	"testing"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

func TestMarketDataRepo(t *testing.T) {
	repo := NewMarketDataRepo()
	data := port.MarketData{
		StockCode: "BMRI",
		Last:      decimal.RequireFromString("1005"),
		PrevClose: decimal.RequireFromString("1000"),
		Volume:    1800,
		UpdatedAt: time.Now().UTC(),
	}

	if err := repo.Save(data); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.Get("BMRI")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Last.String() != "1005" || got.PrevClose.String() != "1000" || got.Volume != 1800 {
		t.Fatalf("Get = %+v, want saved market data", got)
	}

	updated, err := repo.SetLast("BMRI", decimal.RequireFromString("1010"), time.Now().UTC())
	if err != nil {
		t.Fatalf("SetLast: %v", err)
	}
	if updated.Last.String() != "1010" || updated.Volume != 1800 || updated.PrevClose.String() != "1000" {
		t.Fatalf("SetLast = %+v, want last=1010 and preserved volume/prev close", updated)
	}

	if _, err := repo.SetLast("BBCA", decimal.RequireFromString("1"), time.Now()); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("SetLast missing err = %v, want ErrDataNotFound", err)
	}
	if _, err := repo.SetLast("", decimal.RequireFromString("1"), time.Now()); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetLast empty code err = %v, want ErrInvalidArgument", err)
	}

	if _, err := repo.Get("BBCA"); !errors.Is(err, ErrDataNotFound) {
		t.Fatalf("Get missing err = %v, want ErrDataNotFound", err)
	}
	if _, err := repo.Get(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Get empty code err = %v, want ErrInvalidArgument", err)
	}
	if err := repo.Save(port.MarketData{}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("Save empty err = %v, want ErrInvalidData", err)
	}
}
