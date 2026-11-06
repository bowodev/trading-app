package applications

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/bowodev/trading-backend/apps/internal/port"
	"github.com/shopspring/decimal"
)

const (
	// DefaultPriceSimInterval is how often simulated prices move when no
	// trade arrives.
	DefaultPriceSimInterval = 3 * time.Second
	// DefaultPriceVolatilityBps caps one simulated move at 0.40%.
	DefaultPriceVolatilityBps = 40
)

// defaultBasePrices gives seeded stocks a realistic starting point. Unknown
// codes fall back to 1000.
var defaultBasePrices = map[string]string{
	"BMRI": "6000",
	"BBCA": "9000",
	"BBRI": "4500",
	"TLKM": "3000",
}

// PriceSimulator periodically applies a small random walk to the last price
// and publishes a market.ticker event. Trades still overwrite the last price,
// so simulated and real (matching-engine) prices share one source of truth.
type PriceSimulator struct {
	marketData port.IMarketDataRepository
	publisher  port.IEventPublisher
	codes      []string

	interval      time.Duration
	volatilityBps int
	rng           *rand.Rand

	mu      sync.Mutex
	started bool
	closed  bool

	stop chan struct{}
	done chan struct{}
}

func NewPriceSimulator(
	marketData port.IMarketDataRepository,
	publisher port.IEventPublisher,
	codes []string,
	interval time.Duration,
	volatilityBps int,
) *PriceSimulator {
	if interval <= 0 {
		interval = DefaultPriceSimInterval
	}
	if volatilityBps <= 0 {
		volatilityBps = DefaultPriceVolatilityBps
	}
	if volatilityBps > 1000 {
		volatilityBps = 1000
	}

	return &PriceSimulator{
		marketData:    marketData,
		publisher:     publisher,
		codes:         normalizeSimulationCodes(codes),
		interval:      interval,
		volatilityBps: volatilityBps,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Seed writes a starting last price and previous close for every configured
// stock. It also repairs market data that was saved without a previous close.
func (s *PriceSimulator) Seed() error {
	now := time.Now().UTC()

	for _, code := range s.codes {
		base := basePrice(code)

		data, err := s.marketData.Get(code)
		switch {
		case err == nil:
			changed := false
			if data.Last.IsZero() {
				data.Last = base
				changed = true
			}
			if data.PrevClose.IsZero() {
				data.PrevClose = data.Last
				changed = true
			}
			if !changed {
				continue
			}

			data.UpdatedAt = now
			if err := s.marketData.Save(data); err != nil {
				return err
			}
		case errors.Is(err, port.ErrNotFound):
			if err := s.marketData.Save(port.MarketData{
				StockCode: code,
				Last:      base,
				PrevClose: base,
				UpdatedAt: now,
			}); err != nil {
				return err
			}
		default:
			return err
		}
	}

	return nil
}

// Start launches the periodic simulation goroutine. It is safe to call more
// than once.
func (s *PriceSimulator) Start() {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	go s.loop()
}

// Close stops the simulator and waits for its goroutine to exit.
func (s *PriceSimulator) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	started := s.started
	s.mu.Unlock()

	if !started {
		return
	}

	close(s.stop)
	<-s.done
}

func (s *PriceSimulator) loop() {
	defer close(s.done)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.step()
		case <-s.stop:
			return
		}
	}
}

// step moves every stock once and publishes a ticker per stock. It runs only
// from the simulator goroutine, so the random source needs no extra lock.
func (s *PriceSimulator) step() {
	now := time.Now().UTC()

	for _, code := range s.codes {
		data, err := s.marketData.Get(code)
		if err != nil {
			continue
		}

		last := data.Last
		if last.IsZero() {
			last = data.PrevClose
		}
		if last.IsZero() {
			last = basePrice(code)
		}

		deltaBps := s.rng.Intn(2*s.volatilityBps+1) - s.volatilityBps
		if deltaBps == 0 {
			deltaBps = 1
		}

		next := last.
			Mul(decimal.NewFromInt(int64(10000 + deltaBps))).
			Div(decimal.NewFromInt(10000)).
			Round(2)
		if !next.IsPositive() {
			next = decimal.NewFromInt(1)
		}

		updated, err := s.marketData.SetLast(code, next, now)
		if err != nil {
			continue
		}

		change := decimal.Zero
		if !updated.PrevClose.IsZero() {
			change = updated.Last.Sub(updated.PrevClose)
		}

		if s.publisher != nil {
			_ = s.publisher.Publish(port.MarketTickerEvent{
				StockCode: updated.StockCode,
				Last:      updated.Last,
				Change:    change,
				Volume:    updated.Volume,
			})
		}
	}
}

func normalizeSimulationCodes(codes []string) []string {
	seen := make(map[string]struct{}, len(codes))
	normalized := make([]string, 0, len(codes))

	for _, raw := range codes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		if _, found := seen[code]; found {
			continue
		}

		seen[code] = struct{}{}
		normalized = append(normalized, code)
	}

	return normalized
}

func basePrice(code string) decimal.Decimal {
	if raw, found := defaultBasePrices[strings.ToUpper(code)]; found {
		if price, err := decimal.NewFromString(raw); err == nil {
			return price
		}
	}

	return decimal.NewFromInt(1000)
}
