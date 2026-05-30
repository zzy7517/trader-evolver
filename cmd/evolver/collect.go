package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"trader-evolver/internal/collectors"
	"trader-evolver/internal/store"
)

// CollectConfig holds the configuration for the collect command.
type CollectConfig struct {
	DBPath string
	// StartDate is the earliest date to fetch (YYYY-MM-DD). Default: 2020-01-01.
	StartDate string
	// Incremental: if true, resume from latest stored data.
	Incremental bool
}

// DefaultCryptoSymbols are the Binance USDT-M futures symbols to collect.
var DefaultCryptoSymbols = []struct {
	InstrumentKey string
	Symbol        string
}{
	{"btc:usdt", "BTCUSDT"},
	{"eth:usdt", "ETHUSDT"},
	{"sol:usdt", "SOLUSDT"},
}

// DefaultStockSymbols are Yahoo Finance symbols stored as Candles.
var DefaultStockSymbols = []struct {
	InstrumentKey string
	YahooSymbol   string
}{
	{"AAPL", "AAPL"},
	{"MSFT", "MSFT"},
	{"NVDA", "NVDA"},
	{"AVGO", "AVGO"},
	{"TSLA", "TSLA"},
	{"SPY", "SPY"},
	{"QQQ", "QQQ"},
	{"MSTR", "MSTR"},
}

// DefaultMacroSeries are Yahoo symbols stored as DailyMacro.
var DefaultMacroSeries = []struct {
	Series      string
	YahooSymbol string
}{
	{"VIX", "^VIX"},
	{"DXY", "DX-Y.NYB"},
	{"SPX", "^GSPC"},
}

func runCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	cfg := CollectConfig{}
	fs.StringVar(&cfg.DBPath, "db", "./data/evolver.db", "Path to SQLite database")
	fs.StringVar(&cfg.StartDate, "start", "2020-01-01", "Earliest date to fetch (YYYY-MM-DD)")
	fs.BoolVar(&cfg.Incremental, "incremental", true, "Resume from latest stored data")
	fs.Parse(args)

	// Parse start date
	startTime, err := time.Parse("2006-01-02", cfg.StartDate)
	if err != nil {
		return fmt.Errorf("invalid start date %q: %w", cfg.StartDate, err)
	}
	startMs := startTime.UnixMilli()
	endMs := time.Now().UnixMilli()

	// Context with interrupt
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Open store
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	fmt.Printf("evolver collect: db=%s start=%s incremental=%v\n", cfg.DBPath, cfg.StartDate, cfg.Incremental)
	fmt.Println("─────────────────────────────────────────────")

	// ── 1. Crypto (Binance) ──
	fmt.Println("\n[Binance] Fetching crypto futures (1d)...")
	bc := collectors.NewBinanceCollector()
	for _, sym := range DefaultCryptoSymbols {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var n int
		if cfg.Incremental {
			n, err = bc.CollectIncremental(ctx, st, sym.InstrumentKey, sym.Symbol, "1d", startMs, endMs)
		} else {
			n, err = bc.Collect(ctx, st, sym.InstrumentKey, sym.Symbol, "1d", startMs, endMs)
		}
		if err != nil {
			fmt.Printf("  ⚠ %s: %v\n", sym.InstrumentKey, err)
			continue
		}
		fmt.Printf("  ✓ %s: %d candles\n", sym.InstrumentKey, n)
	}

	// ── 2. Stocks (Yahoo) ──
	fmt.Println("\n[Yahoo] Fetching stock/ETF daily candles...")
	yc := collectors.NewYahooCollector()
	for _, sym := range DefaultStockSymbols {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var n int
		if cfg.Incremental {
			n, err = yc.CollectCandlesIncremental(ctx, st, sym.InstrumentKey, sym.YahooSymbol, startMs, endMs)
		} else {
			n, err = yc.CollectCandles(ctx, st, sym.InstrumentKey, sym.YahooSymbol, startMs, endMs)
		}
		if err != nil {
			fmt.Printf("  ⚠ %s: %v\n", sym.InstrumentKey, err)
			continue
		}
		fmt.Printf("  ✓ %s: %d candles\n", sym.InstrumentKey, n)
		// Throttle between Yahoo requests
		time.Sleep(yc.PageDelay)
	}

	// ── 3. Macro series (Yahoo → DailyMacro) ──
	fmt.Println("\n[Yahoo] Fetching macro series (VIX/DXY/SPX)...")
	for _, ms := range DefaultMacroSeries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := yc.CollectMacro(ctx, st, ms.Series, ms.YahooSymbol, startMs, endMs)
		if err != nil {
			fmt.Printf("  ⚠ %s: %v\n", ms.Series, err)
			continue
		}
		fmt.Printf("  ✓ %s: %d rows\n", ms.Series, n)
		time.Sleep(yc.PageDelay)
	}

	// ── 4. Fear & Greed (alternative.me) ──
	fmt.Println("\n[alternative.me] Fetching Fear & Greed Index...")
	fc := collectors.NewFearGreedCollector()
	var fgCount int
	if cfg.Incremental {
		fgCount, err = fc.CollectIncremental(ctx, st)
	} else {
		fgCount, err = fc.Collect(ctx, st)
	}
	if err != nil {
		fmt.Printf("  ⚠ Fear&Greed: %v\n", err)
	} else {
		fmt.Printf("  ✓ Fear&Greed: %d entries\n", fgCount)
	}

	fmt.Println("\n─────────────────────────────────────────────")
	fmt.Println("Collection complete.")
	return nil
}
