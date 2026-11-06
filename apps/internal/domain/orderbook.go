package domain

type OrderBook struct {
	Stock Stock
	Asks  map[string][]Order
	Bids  map[string][]Order
}
