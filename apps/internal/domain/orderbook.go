package domain

type OrderBook struct {
	Stock Stock
	Asks  map[string][]Order // map[price] []orders
	Bids  map[string][]Order // map[price] []orders
}
