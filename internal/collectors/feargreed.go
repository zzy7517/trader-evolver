// Fear & Greed Index collector — fetches from alternative.me API.
//
// Endpoint: https://api.alternative.me/fng/?limit=0&format=json
// With limit=0, returns the full history (all available data points).
// Each data point has: {value, value_classification, timestamp (Unix seconds)}.
//
// Stored via store.UpsertFearGreed([]types.FearGreed).
package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"trader-evolver/internal/store"
	"trader-evolver/internal/types"
)

// DefaultFearGreedBaseURL is the alternative.me Fear & Greed API endpoint.
const DefaultFearGreedBaseURL = "https://api.alternative.me"

// FearGreedCollector fetches the Crypto Fear & Greed Index.
type FearGreedCollector struct {
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
}

// NewFearGreedCollector returns a collector with sane defaults.
func NewFearGreedCollector() *FearGreedCollector {
	return &FearGreedCollector{
		BaseURL:    DefaultFearGreedBaseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 3,
	}
}

// fearGreedResponse is the alternative.me JSON structure.
type fearGreedResponse struct {
	Name     string           `json:"name"`
	Data     []fearGreedEntry `json:"data"`
	Metadata struct {
		Error *string `json:"error"`
	} `json:"metadata"`
}

type fearGreedEntry struct {
	Value               string `json:"value"`
	ValueClassification string `json:"value_classification"`
	Timestamp           string `json:"timestamp"` // Unix seconds as string
}

// FetchAll fetches the full Fear & Greed history (limit=0).
func (c *FearGreedCollector) FetchAll(ctx context.Context) ([]types.FearGreed, error) {
	endpoint := fmt.Sprintf("%s/fng/?limit=0&format=json", c.BaseURL)
	body, err := c.doRequestWithRetry(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return parseFearGreed(body)
}

// FetchRecent fetches the last N days of Fear & Greed data.
func (c *FearGreedCollector) FetchRecent(ctx context.Context, days int) ([]types.FearGreed, error) {
	endpoint := fmt.Sprintf("%s/fng/?limit=%d&format=json", c.BaseURL, days)
	body, err := c.doRequestWithRetry(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return parseFearGreed(body)
}

// Collect fetches full history and upserts into the store.
func (c *FearGreedCollector) Collect(ctx context.Context, st *store.Store) (int, error) {
	data, err := c.FetchAll(ctx)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	if err := st.UpsertFearGreed(data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// CollectIncremental fetches only recent data (last 30 days) for incremental updates.
func (c *FearGreedCollector) CollectIncremental(ctx context.Context, st *store.Store) (int, error) {
	data, err := c.FetchRecent(ctx, 30)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	if err := st.UpsertFearGreed(data); err != nil {
		return 0, err
	}
	return len(data), nil
}

// doRequestWithRetry performs an HTTP GET with retry/backoff.
func (c *FearGreedCollector) doRequestWithRetry(ctx context.Context, endpoint string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("feargreed status %d: %s", resp.StatusCode, truncateBody(body))
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("feargreed after %d retries: %w", c.MaxRetries, lastErr)
}

// parseFearGreed decodes the alternative.me response into typed entries.
func parseFearGreed(body []byte) ([]types.FearGreed, error) {
	var resp fearGreedResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("feargreed: decode: %w", err)
	}
	if resp.Metadata.Error != nil && *resp.Metadata.Error != "" {
		return nil, fmt.Errorf("feargreed API error: %s", *resp.Metadata.Error)
	}

	out := make([]types.FearGreed, 0, len(resp.Data))
	for _, entry := range resp.Data {
		val, err := strconv.Atoi(entry.Value)
		if err != nil {
			continue // skip malformed entries
		}
		tsSec, err := strconv.ParseInt(entry.Timestamp, 10, 64)
		if err != nil {
			continue
		}
		// Normalize to midnight UTC
		dayMs := normalizeToMidnightUTC(tsSec)

		out = append(out, types.FearGreed{
			DateMs:         dayMs,
			Value:          val,
			Classification: entry.ValueClassification,
		})
	}
	return out, nil
}
