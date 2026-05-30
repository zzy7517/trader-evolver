package collectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"trader-evolver/internal/store"
)

// sampleFearGreedResponse builds a minimal alternative.me JSON with N entries.
func sampleFearGreedResponse(n int) []byte {
	baseTs := int64(1700000000) // Nov 2023
	entries := make([]map[string]string, n)
	classifications := []string{"Extreme Fear", "Fear", "Neutral", "Greed", "Extreme Greed"}

	for i := 0; i < n; i++ {
		val := 20 + i*15 // 20, 35, 50, 65, 80...
		if val > 100 {
			val = 100
		}
		entries[i] = map[string]string{
			"value":                strconv.Itoa(val),
			"value_classification": classifications[i%len(classifications)],
			"timestamp":           strconv.FormatInt(baseTs+int64(i*86400), 10),
		}
	}

	resp := map[string]any{
		"name": "Fear and Greed Index",
		"data": entries,
		"metadata": map[string]any{
			"error": nil,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestParseFearGreed(t *testing.T) {
	body := sampleFearGreedResponse(5)
	data, err := parseFearGreed(body)
	if err != nil {
		t.Fatalf("parseFearGreed error: %v", err)
	}
	if len(data) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(data))
	}
	for i, d := range data {
		if d.Value < 0 || d.Value > 100 {
			t.Errorf("entry %d: value %d out of range", i, d.Value)
		}
		if d.Classification == "" {
			t.Errorf("entry %d: empty classification", i)
		}
		if d.DateMs <= 0 {
			t.Errorf("entry %d: invalid dateMs %d", i, d.DateMs)
		}
	}
}

func TestParseFearGreed_MidnightNormalization(t *testing.T) {
	// Timestamps should be normalized to midnight UTC
	body := sampleFearGreedResponse(1)
	data, err := parseFearGreed(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 {
		t.Fatal("expected 1 entry")
	}
	// Check that the ms timestamp represents midnight
	ts := time.UnixMilli(data[0].DateMs).UTC()
	if ts.Hour() != 0 || ts.Minute() != 0 || ts.Second() != 0 {
		t.Errorf("expected midnight UTC, got %s", ts)
	}
}

func TestFearGreedFetchAll_HTTPTest(t *testing.T) {
	body := sampleFearGreedResponse(10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify limit=0 is requested
		q := r.URL.Query()
		if q.Get("limit") != "0" {
			t.Errorf("expected limit=0, got %s", q.Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	fc := &FearGreedCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	data, err := fc.FetchAll(ctx)
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(data) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(data))
	}
}

func TestFearGreedFetchRecent(t *testing.T) {
	body := sampleFearGreedResponse(7)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("limit") != "7" {
			t.Errorf("expected limit=7, got %s", q.Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	fc := &FearGreedCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	data, err := fc.FetchRecent(ctx, 7)
	if err != nil {
		t.Fatalf("FetchRecent error: %v", err)
	}
	if len(data) != 7 {
		t.Fatalf("expected 7 entries, got %d", len(data))
	}
}

func TestFearGreedCollect_StoreIntegration(t *testing.T) {
	body := sampleFearGreedResponse(5)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	dbPath := t.TempDir() + "/test.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fc := &FearGreedCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	n, err := fc.Collect(ctx, st)
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5, got %d", n)
	}

	// Verify via FearGreedCount
	count, err := st.FearGreedCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected count=5, got %d", count)
	}

	// Verify as-of lookup
	// Last entry timestamp
	lastTs := normalizeToMidnightUTC(1700000000 + 4*86400)
	val, found, err := st.FearGreedAsOf(lastTs)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected fear&greed entry, got not found")
	}
	if val < 0 || val > 100 {
		t.Errorf("unexpected value: %d", val)
	}
}

func TestFearGreedRetryOn500(t *testing.T) {
	attempts := 0
	body := sampleFearGreedResponse(3)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	fc := &FearGreedCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 3,
	}

	ctx := context.Background()
	data, err := fc.FetchAll(ctx)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if len(data) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(data))
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestFearGreedCollectIncremental(t *testing.T) {
	body := sampleFearGreedResponse(3)
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Incremental requests limit=30
		q := r.URL.Query()
		if q.Get("limit") != "30" {
			t.Errorf("incremental should use limit=30, got %s", q.Get("limit"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	dbPath := t.TempDir() + "/test.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fc := &FearGreedCollector{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		MaxRetries: 1,
	}

	ctx := context.Background()
	n, err := fc.CollectIncremental(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3, got %d", n)
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}
