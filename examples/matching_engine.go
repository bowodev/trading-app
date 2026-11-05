package examples

import (
	"fmt"
	"sync"
	"time"
)

type Order struct {
	ID      string
	UserID  string
	Price   float64
	Amount  float64
	Status  string // pending | matched | partial
	Action  string // buy | sell
	OrderAt time.Time
}

var SellOrders []Order
var BuyOrders []Order
var mu sync.Mutex

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func printBook(title string) {
	fmt.Println("====", title, "====")
	fmt.Println("BUY:")
	for _, o := range BuyOrders {
		fmt.Printf("  %s price=%.2f amt=%.2f\n", o.ID, o.Price, o.Amount)
	}
	fmt.Println("SELL:")
	for _, o := range SellOrders {
		fmt.Printf("  %s price=%.2f amt=%.2f\n", o.ID, o.Price, o.Amount)
	}
	fmt.Println()
}

func seedOrderBooks() {
	base := time.Now().Add(-time.Hour)

	BuyOrders = []Order{
		{ID: "buy-1", UserID: "b1", Price: 110, Amount: 50, Action: "buy", Status: "pending", OrderAt: base.Add(1 * time.Minute)},
		{ID: "buy-2", UserID: "b2", Price: 105, Amount: 30, Action: "buy", Status: "pending", OrderAt: base.Add(2 * time.Minute)},
		{ID: "buy-3", UserID: "b3", Price: 100, Amount: 20, Action: "buy", Status: "pending", OrderAt: base.Add(3 * time.Minute)},
	}

	SellOrders = []Order{
		{ID: "sell-1", UserID: "s1", Price: 108, Amount: 40, Action: "sell", Status: "pending", OrderAt: base.Add(1 * time.Minute)},
		{ID: "sell-2", UserID: "s2", Price: 112, Amount: 25, Action: "sell", Status: "pending", OrderAt: base.Add(2 * time.Minute)},
		{ID: "sell-3", UserID: "s3", Price: 120, Amount: 15, Action: "sell", Status: "pending", OrderAt: base.Add(3 * time.Minute)},
	}
}

func insertBuySorted(o Order) {
	i := 0
	for i < len(BuyOrders) {
		if o.Price > BuyOrders[i].Price {
			break
		}
		if o.Price == BuyOrders[i].Price && o.OrderAt.Before(BuyOrders[i].OrderAt) {
			break
		}
		i++
	}
	BuyOrders = append(BuyOrders, Order{})
	copy(BuyOrders[i+1:], BuyOrders[i:])
	BuyOrders[i] = o
}

func insertSellSorted(o Order) {
	i := 0
	for i < len(SellOrders) {
		if o.Price < SellOrders[i].Price {
			break
		}
		if o.Price == SellOrders[i].Price && o.OrderAt.Before(SellOrders[i].OrderAt) {
			break
		}
		i++
	}
	SellOrders = append(SellOrders, Order{})
	copy(SellOrders[i+1:], SellOrders[i:])
	SellOrders[i] = o
}

func handleSellOrder(req Order) {
	mu.Lock()
	defer mu.Unlock()

	original := req.Amount
	i := 0
	for i < len(BuyOrders) && req.Amount > 0 {
		buy := &BuyOrders[i]
		if buy.Price < req.Price {
			break
		}
		qty := min(req.Amount, buy.Amount)
		req.Amount -= qty
		buy.Amount -= qty
		fmt.Printf("TRADE  sell=%s  buy=%s  qty=%.2f  @ %.2f\n",
			req.ID, buy.ID, qty, buy.Price)
		if buy.Amount == 0 {
			BuyOrders = append(BuyOrders[:i], BuyOrders[i+1:]...)
			continue
		}
		i++
	}
	filled := original - req.Amount
	if req.Amount == 0 {
		req.Status = "matched"
		fmt.Println("ORDER", req.ID, "→ matched")
	} else if filled > 0 {
		req.Status = "partial"
		req.OrderAt = time.Now()
		insertSellSorted(req)
		fmt.Printf("ORDER %s → partial, sisa %.2f ke SELL book\n", req.ID, req.Amount)
	} else {
		req.Status = "pending"
		req.OrderAt = time.Now()
		insertSellSorted(req)
		fmt.Printf("ORDER %s → no match, masuk SELL book\n", req.ID)
	}
}

func handleBuyOrder(req Order) {
	mu.Lock()
	defer mu.Unlock()

	original := req.Amount
	i := 0

	for i < len(SellOrders) && req.Amount > 0 {
		sell := &SellOrders[i]

		// sell harus <= buy price
		if sell.Price > req.Price {
			break // book ASC → sisanya lebih mahal, stop
		}

		qty := min(req.Amount, sell.Amount)
		req.Amount -= qty
		sell.Amount -= qty

		fmt.Printf("TRADE  buy=%s  sell=%s  qty=%.2f  @ %.2f\n",
			req.ID, sell.ID, qty, sell.Price)

		if sell.Amount == 0 {
			SellOrders = append(SellOrders[:i], SellOrders[i+1:]...)
			continue
		}
		i++
	}

	filled := original - req.Amount
	if req.Amount == 0 {
		req.Status = "matched"
		fmt.Println("ORDER", req.ID, "→ matched")
	} else if filled > 0 {
		req.Status = "partial"
		req.OrderAt = time.Now()
		insertBuySorted(req)
		fmt.Printf("ORDER %s → partial, sisa %.2f ke BUY book\n", req.ID, req.Amount)
	} else {
		req.Status = "pending"
		req.OrderAt = time.Now()
		insertBuySorted(req)
		fmt.Printf("ORDER %s → no match, masuk BUY book\n", req.ID)
	}
}

func main() {
	seedOrderBooks()
	queue := make(chan Order, 10)

	var wg sync.WaitGroup
	wg.Add(1)

	requests := []Order{
		{ID: "req-S1", UserID: "u1", Action: "sell", Price: 105, Amount: 60, Status: "pending"},
		{ID: "req-B1", UserID: "u2", Action: "buy", Price: 115, Amount: 30, Status: "pending"},
		{ID: "req-S2", UserID: "u3", Action: "sell", Price: 999, Amount: 10, Status: "pending"},
	}

	go matchingEngine(queue, &wg)

	for _, req := range requests {
		fmt.Println("kirim ke queue:", req.ID)
		queue <- req
	}

	close(queue)
	wg.Wait()
	printBook("akhir")
}

func matchingEngine(queue <-chan Order, wg *sync.WaitGroup) {
	defer wg.Done()
	for order := range queue {
		fmt.Printf("\n>> engine terima: %s %s price=%.2f amt=%.2f\n",
			order.ID, order.Action, order.Price, order.Amount)
		switch order.Action {
		case "sell":
			handleSellOrder(order)
		case "buy":
			handleBuyOrder(order)
		default:
			fmt.Println("action tidak dikenal:", order.Action)
		}
	}
	fmt.Println("\nengine berhenti (queue closed)")
}
