package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"trader-evolver/internal/store"
)

// ReportOutput is the JSON structure for the report.
type ReportOutput struct {
	Period           string             `json:"period"`
	TradingDays      int                `json:"tradingDays"`
	Instrument       string             `json:"instrument"`
	FinalWeights     map[string]float64 `json:"finalWeights"`
	TotalRecommendations int            `json:"totalRecommendations"`
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)

	var (
		dbPath     string
		instrument string
		outputJSON bool
	)

	fs.StringVar(&dbPath, "db", "./data/evolver.db", "Path to SQLite database")
	fs.StringVar(&instrument, "instrument", "btc:usdt", "Instrument key")
	fs.BoolVar(&outputJSON, "json", false, "Output as JSON")
	fs.Parse(args)

	// Open store
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Get coverage info
	cov, err := st.CandleCoverage(instrument, "1d")
	if err != nil {
		return fmt.Errorf("coverage: %w", err)
	}

	if outputJSON {
		report := ReportOutput{
			Period:      fmt.Sprintf("coverage: %d bars", cov.Count),
			TradingDays: int(cov.Count),
			Instrument:  instrument,
		}
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Println("evolver report")
	fmt.Println("─────────────────────────────────────────────")
	fmt.Printf("Instrument: %s\n", instrument)
	fmt.Printf("Database: %s\n", dbPath)
	fmt.Printf("Candle coverage: %d bars\n", cov.Count)

	if cov.Count > 0 {
		fmt.Printf("  First: %d\n", cov.FirstOpenMs)
		fmt.Printf("  Last:  %d\n", cov.LastOpenMs)
	}

	// Macro coverage
	for _, series := range []string{"VIX", "DXY", "SPX"} {
		_, found, _ := st.MacroAsOf(series, 9999999999999) // far future to get latest
		if found {
			fmt.Printf("Macro %s: available\n", series)
		} else {
			fmt.Printf("Macro %s: not loaded\n", series)
		}
	}

	// Fear & Greed
	fgCount, _ := st.FearGreedCount()
	fmt.Printf("Fear & Greed: %d entries\n", fgCount)

	fmt.Println("\n─────────────────────────────────────────────")
	fmt.Println("Run 'evolver backtest' to generate full results.")

	return nil
}

// ensure os is used
var _ = os.Stdout
