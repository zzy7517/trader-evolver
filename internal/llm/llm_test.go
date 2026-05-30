package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Compile-time checks that both providers satisfy Provider.
var _ Provider = (*MockProvider)(nil)
var _ Provider = (*CodexProvider)(nil)

func TestMockProviderModuleOutput(t *testing.T) {
	m := NewMockProvider()
	content, tokens, err := m.Call(context.Background(), "ict persona", "## K线数据\nfoo\n只输出JSON")
	if err != nil {
		t.Fatal(err)
	}
	if tokens <= 0 {
		t.Fatalf("expected positive tokens, got %d", tokens)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("mock output not valid JSON: %v\n%s", err, content)
	}
	sig, _ := out["signal"].(string)
	if sig != "LONG" && sig != "SHORT" && sig != "NEUTRAL" {
		t.Fatalf("unexpected signal %q", sig)
	}
	// Deterministic: same input -> same output.
	content2, _, _ := m.Call(context.Background(), "ict persona", "## K线数据\nfoo\n只输出JSON")
	if content != content2 {
		t.Fatal("mock provider not deterministic")
	}
}

func TestMockProviderCRO(t *testing.T) {
	m := NewMockProvider()
	content, _, _ := m.Call(context.Background(), "risk officer", "## 候选交易决策\n\nOPEN_LONG\n")
	var out map[string]any
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		t.Fatalf("CRO output not JSON: %v", err)
	}
	if out["approved"] != true {
		t.Fatalf("expected approved=true, got %v", out["approved"])
	}
}

func TestParseCodexSSE(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hi "}`,
		`data: {"type":"response.output_text.delta","delta":"there"}`,
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":4}}}`,
		`data: [DONE]`,
	}, "\n")
	content, tokens, err := parseCodexSSE(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if content != "hi there" {
		t.Fatalf("content=%q", content)
	}
	if tokens != 7 {
		t.Fatalf("tokens=%d want 7", tokens)
	}
}

func TestParseCodexSSEDoneFallback(t *testing.T) {
	// No deltas; output_text.done carries the full text.
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.done","text":"full"}`,
		`data: {"type":"response.completed","response":{"usage":{"total_tokens":12}}}`,
	}, "\n")
	content, tokens, err := parseCodexSSE(strings.NewReader(stream))
	if err != nil || content != "full" || tokens != 12 {
		t.Fatalf("content=%q tokens=%d err=%v", content, tokens, err)
	}
}

func TestJWTHelpers(t *testing.T) {
	// payload = {"chatgpt_account_id":"acc123","exp":9999999999}
	payload := "eyJjaGF0Z3B0X2FjY291bnRfaWQiOiJhY2MxMjMiLCJleHAiOjk5OTk5OTk5OTl9"
	token := "h." + payload + ".s"
	if got := accountIDFromToken(token); got != "acc123" {
		t.Fatalf("accountIDFromToken got %q want acc123", got)
	}
	if accessTokenIsExpired(token) {
		t.Fatal("far-future exp should not be expired")
	}
}
