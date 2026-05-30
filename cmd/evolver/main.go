// Command evolver is the CLI entrypoint for the trader-evolver system.
//
// Usage:
//
//	evolver collect   # fetch multi-year history into the store
//	evolver backtest  # replay the pipeline over history, evolve Darwin weights
//	evolver report    # render backtest results
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "collect":
		if err := runCollect(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "backtest":
		if err := runBacktest(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "report":
		if err := runReport(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`evolver — historical backtesting & evolution for multi-method trading pipelines

Usage:
  evolver <command> [options]

Commands:
  collect    Fetch multi-year history into the local SQLite store
  backtest   Replay the pipeline over history, evolve Darwin weights
  report     Render backtest results (equity curve, agent weights, modifications)

Options:
  --db <path>    Path to SQLite database (default: ./data/evolver.db)

Run 'evolver <command> --help' for command-specific options.`)
}
