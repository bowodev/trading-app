package domain

import (
	"time"

	"github.com/oklog/ulid/v2"
)

type Trade struct {
	ID        ulid.ULID
	Stock     Stock
	Price     Price
	Quantity  int
	BuyOrder  Order
	SellOrder Order

	CreatedAt time.Time
}
