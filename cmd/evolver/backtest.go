package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"trader-evolver/internal/backtest"
	"trader-evolver/internal/llm"
	"trader-evolver/internal/store"
)

func runBacktest(args []string) error {
	fs := flag.NewFlagSet("backtest", flag.ExitOnError)

	var (
		dbPath     string
		instrument string
		startDate  string
		endDate    string
		promptDir  string
		useMock    bool
	)

	fs.StringVar(&dbPath, "db", "./data/evolver.db", "Path to SQLite database")
	fs.StringVar(&instrument, "instrument", "btc:usdt", "Instrument key to backtest")
	fs.StringVar(&startDate, "start", "2024-01-01", "Backtest start date (YYYY-MM-DD)")
	fs.StringVar(&endDate, "end", "", "Backtest end date (YYYY-MM-DD, default: today)")
	fs.StringVar(&promptDir, "prompts", "./prompts", "Path to prompt template directory")
	fs.BoolVar(&useMock, "mock", false, "Use mock provider (no API calls)")
	fs.Parse(args)

	// Parse dates
	startTime, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return fmt.Errorf("invalid start date: %w", err)
	}
	var endTime time.Time
	if endDate == "" {
		endTime = time.Now().UTC()
	} else {
		endTime, err = time.Parse("2006-01-02", endDate)
		if err != nil {
			return fmt.Errorf("invalid end date: %w", err)
		}
	}

	// Context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Open store
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Provider
	var provider llm.Provider
	if useMock {
		provider = llm.NewMockProvider()
		fmt.Println("[backtest] Using MOCK provider (no API calls)")
	} else {
		provider, err = llm.NewCodexProvider()
		if err != nil {
			return fmt.Errorf("codex provider: %w (use --mock for offline testing)", err)
		}
	}

	// Config
	cfg := backtest.DefaultConfig()
	cfg.InstrumentKey = instrument
	cfg.StartDateMs = startTime.UnixMilli()
	cfg.EndDateMs = endTime.UnixMilli()
	cfg.PromptDir = promptDir

	fmt.Printf("evolver backtest\n")
	fmt.Printf("  instrument: %s\n", instrument)
	fmt.Printf("  period: %s to %s\n", startDate, endTime.Format("2006-01-02"))
	fmt.Printf("  db: %s\n", dbPath)
	fmt.Printf("  provider: %s\n", provider.Name())
	fmt.Println("─────────────────────────────────────────────")

	// Run engine
	engine := backtest.NewEngine(st, provider, cfg)

	// Progress callback
	engine.OnDayComplete = func(day backtest.DayResult) {
		action := "PASS"
		if day.Decision != nil {
			action = string(day.Decision.Action)
		}
		errStr := ""
		if day.Error != nil {
			errStr = " ERR:" + *day.Error
			if len(errStr) > 50 {
				errStr = errStr[:50] + "..."
			}
		}
		fmt.Printf("  Day %3d | %s | %s | %s%s\n",
			day.Day, day.Date, day.Regime.Market, action, errStr)
	}

	startRun := time.Now()
	results, err := engine.Run(ctx)
	elapsed := time.Since(startRun)

	if err != nil && err != context.Canceled {
		return fmt.Errorf("backtest failed: %w", err)
	}

	// Summary
	fmt.Println("\n─────────────────────────────────────────────")
	fmt.Printf("Backtest complete: %d trading days in %s\n", len(results), elapsed.Round(time.Millisecond))

	recs := engine.Recommendations()
	fmt.Printf("Total recommendations: %d\n", len(recs))

	// Darwin weight summary
	fmt.Println("\nFinal Darwin Weights:")
	for id, w := range engine.DarwinWeightsSnapshot() {
		fmt.Printf("  %-22s %.3f\n", id, w)
	}

	// Count decisions by type
	actionCounts := map[string]int{}
	for _, r := range results {
		if r.Decision != nil {
			actionCounts[string(r.Decision.Action)]++
		} else {
			actionCounts["ERROR"]++
		}
	}
	fmt.Println("\nDecision Summary:")
	for action, count := range actionCounts {
		fmt.Printf("  %-12s %d\n", action, count)
	}

	return nil
}
