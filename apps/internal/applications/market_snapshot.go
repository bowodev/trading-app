package applications

import (
	"errors"

	"github.com/bowodev/trading-backend/apps/internal/domain"
	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

const (
	snapshotDepth     = 10
	defaultTradeLimit = 20
)

type MarketSnapshotQuery struct {
	StockCode      string
	ShowTradeLimit int
}

type MarketSnapshot struct {
	stocks     port.IStockRepository
	books      port.IOrderBookRepository
	trades     port.ITradeRepository
	marketData port.IMarketDataRepository
}

func NewMarketSnapshot(
	stocks port.IStockRepository,
	books port.IOrderBookRepository,
	trades port.ITradeRepository,
	marketData port.IMarketDataRepository,
) *MarketSnapshot {
	return &MarketSnapshot{
		stocks:     stocks,
		books:      books,
		trades:     trades,
		marketData: marketData,
	}
}

func (uc *MarketSnapshot) Execute(query MarketSnapshotQuery) (SnapshotView, error) {
	if query.StockCode == "" {
		return SnapshotView{}, ErrInvalidArgument
	}

	stock, err := uc.stocks.FindByCode(query.StockCode)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return SnapshotView{}, ErrStockNotFound
		}
		return SnapshotView{}, err
	}

	tradeLimit := normalizeLimit(query.ShowTradeLimit, defaultTradeLimit)

	marketData, err := uc.marketData.Get(query.StockCode)
	if err != nil {
		if !errors.Is(err, port.ErrNotFound) {
			return SnapshotView{}, err
		}
		marketData = port.MarketData{
			StockCode: query.StockCode,
			Last:      decimal.Zero,
			PrevClose: decimal.Zero,
		}
	}

	trades, err := uc.trades.ListByStock(query.StockCode, tradeLimit)
	if err != nil {
		return SnapshotView{}, mapInvalidArgument(err)
	}

	bids, asks, err := uc.books.Snapshot(stock.ID, snapshotDepth)
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return SnapshotView{}, err
	}

	last := marketData.Last
	if last.IsZero() && len(trades) > 0 {
		last = decimal.Decimal(trades[0].Price)
	}

	change := decimal.Zero
	if !marketData.PrevClose.IsZero() {
		change = last.Sub(marketData.PrevClose)
	}

	return SnapshotView{
		StockCode: stock.Code,
		Price: PriceView{
			Last:   last.String(),
			Change: change.String(),
			Volume: marketData.Volume,
		},
		OrderBook: OrderBookView{
			Bids: aggregateLevels(bids),
			Asks: aggregateLevels(asks),
		},
		Trades: newTradeViews(trades),
	}, nil
}

func aggregateLevels(orders []domain.Order) []OrderBookLevelView {
	levels := make([]OrderBookLevelView, 0, len(orders))
	for _, order := range orders {
		price := order.Price.String()
		if n := len(levels); n > 0 && levels[n-1].Price == price {
			levels[n-1].Qty += order.Qty
			continue
		}

		levels = append(levels, OrderBookLevelView{Price: price, Qty: order.Qty})
	}

	return levels
}

// OrderBook is the cheaper read used for live depth refreshes.
func (uc *MarketSnapshot) OrderBook(stockCode string) (OrderBookView, error) {
	if stockCode == "" {
		return OrderBookView{}, ErrInvalidArgument
	}

	stock, err := uc.stocks.FindByCode(stockCode)
	if err != nil {
		if errors.Is(err, port.ErrNotFound) {
			return OrderBookView{}, ErrStockNotFound
		}
		return OrderBookView{}, err
	}

	bids, asks, err := uc.books.Snapshot(stock.ID, snapshotDepth)
	if err != nil && !errors.Is(err, port.ErrNotFound) {
		return OrderBookView{}, err
	}

	return OrderBookView{
		Bids: aggregateLevels(bids),
		Asks: aggregateLevels(asks),
	}, nil
}
