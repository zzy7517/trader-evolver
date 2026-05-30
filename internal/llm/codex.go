package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultCodexBaseURL mirrors tradex/config/agent_models.ts DEFAULT_CODEX_BASE_URL.
const DefaultCodexBaseURL = "https://chatgpt.com/backend-api/codex"

// DefaultCodexModel is the model slug used when none is configured.
const DefaultCodexModel = "gpt-5"

// DefaultReasoningEffort matches tradex's medium default.
const DefaultReasoningEffort = "medium"

const (
	maxRetries  = 3
	baseDelayMS = 1000
)

// CodexProvider performs single-shot calls against the Codex Responses API.
//
// It is faithful to tradex/agent/providers/codex.ts:
//   - credential resolution order: explicit APIKey > CODEX_API_KEY env >
//     $CODEX_HOME/auth.json (default ~/.codex/auth.json)
//   - headers: Authorization bearer, originator codex_cli_rs, ChatGPT-Account-ID
//   - payload: { model, input, instructions, store:false, stream:true,
//     reasoning:{effort,summary:auto} }  (no tools — we never tool-call)
//   - retry: exponential backoff on 429/5xx and retryable network errors
//
// It does NOT support base_url overrides beyond the constructor (research tool).
type CodexProvider struct {
	BaseURL         string
	Model           string
	ReasoningEffort string
	Timeout         time.Duration

	accessToken string
	accountID   string
	httpClient  *http.Client
}

// CodexOption configures a CodexProvider.
type CodexOption func(*CodexProvider)

// WithModel overrides the model slug.
func WithModel(model string) CodexOption {
	return func(p *CodexProvider) {
		if model != "" {
			p.Model = model
		}
	}
}

// WithReasoningEffort overrides the reasoning effort (low/medium/high/xhigh).
func WithReasoningEffort(effort string) CodexOption {
	return func(p *CodexProvider) {
		if effort != "" {
			p.ReasoningEffort = effort
		}
	}
}

// WithTimeout sets the per-call timeout.
func WithTimeout(d time.Duration) CodexOption {
	return func(p *CodexProvider) {
		if d > 0 {
			p.Timeout = d
		}
	}
}

// WithBaseURL overrides the Codex base URL (rarely needed).
func WithBaseURL(url string) CodexOption {
	return func(p *CodexProvider) {
		if url != "" {
			p.BaseURL = url
		}
	}
}

// WithAPIKey supplies an explicit access token, bypassing env/auth.json.
func WithAPIKey(key string) CodexOption {
	return func(p *CodexProvider) {
		if key != "" {
			p.accessToken = key
		}
	}
}

// NewCodexProvider resolves credentials and returns a ready provider.
// Returns an error if no usable Codex credential is found or the token expired.
func NewCodexProvider(opts ...CodexOption) (*CodexProvider, error) {
	p := &CodexProvider{
		BaseURL:         DefaultCodexBaseURL,
		Model:           DefaultCodexModel,
		ReasoningEffort: DefaultReasoningEffort,
		Timeout:         120 * time.Second,
	}
	for _, o := range opts {
		o(p)
	}

	if p.accessToken == "" {
		token, account, err := resolveCodexCredentials()
		if err != nil {
			return nil, err
		}
		p.accessToken = token
		p.accountID = account
	}
	if p.accessToken == "" {
		return nil, errors.New("CODEX_API_KEY or Codex CLI auth is required")
	}
	if p.accountID == "" {
		p.accountID = accountIDFromToken(p.accessToken)
	}

	p.httpClient = &http.Client{Timeout: p.Timeout}
	return p, nil
}

// Name implements Provider.
func (p *CodexProvider) Name() string { return "codex:" + p.Model }

// Call implements Provider with a single Responses API request.
func (p *CodexProvider) Call(ctx context.Context, systemPrompt, userPrompt string) (string, int, error) {
	payload := map[string]any{
		"model": p.Model,
		"input": []map[string]any{
			{
				"role":    "user",
				"content": []map[string]any{{"type": "input_text", "text": userPrompt}},
			},
		},
		"instructions": systemPrompt,
		"store":        false,
		"stream":       true,
		"reasoning": map[string]any{
			"effort":  p.ReasoningEffort,
			"summary": "auto",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", 0, fmt.Errorf("codex: marshal payload: %w", err)
	}

	resp, err := p.doWithRetry(ctx, body)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	return parseCodexSSE(resp.Body)
}

func (p *CodexProvider) doWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	url := strings.TrimRight(p.BaseURL, "/") + "/responses"
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range p.headers() {
			req.Header.Set(k, v)
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if strings.Contains(strings.ToLower(err.Error()), "usage limit") {
				return nil, err
			}
			if attempt < maxRetries && isRetryableNetworkError(err) {
				sleepBackoff(ctx, attempt)
				continue
			}
			return nil, err
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}

		errText, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		if attempt < maxRetries && isRetryableStatus(resp.StatusCode, string(errText)) {
			sleepBackoff(ctx, attempt)
			continue
		}
		return nil, fmt.Errorf("Codex API %d: %s", resp.StatusCode, string(errText))
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("codex: failed after retries")
}

func (p *CodexProvider) headers() map[string]string {
	h := map[string]string{
		"Authorization": "Bearer " + p.accessToken,
		"Content-Type":  "application/json",
		"User-Agent":    "codex_cli_rs/0.0.0 (trader-evolver)",
		"originator":    "codex_cli_rs",
		"Accept":        "text/event-stream",
	}
	if p.accountID != "" {
		h["ChatGPT-Account-ID"] = p.accountID
	}
	return h
}

func sleepBackoff(ctx context.Context, attempt int) {
	d := time.Duration(baseDelayMS*(1<<attempt)) * time.Millisecond
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func isRetryableStatus(status int, errText string) bool {
	switch status {
	case 429, 500, 502, 503, 504:
		return true
	}
	return regexp.MustCompile(`(?i)rate.?limit|overloaded|service.?unavailable|upstream.?connect`).MatchString(errText)
}

func isRetryableNetworkError(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, frag := range []string{
		"unexpected eof", "connection reset", "econnreset", "econnrefused",
		"socket hang up", "network", "timeout", "etimedout", "fetch failed",
		"eof", "broken pipe", "i/o timeout",
	} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}

// parseCodexSSE consumes the Responses API SSE stream, accumulating
// response.output_text deltas and reading usage from response.completed.
// Mirrors the event handling in tradex/agent/providers/codex.ts.
func parseCodexSSE(r io.Reader) (string, int, error) {
	var text strings.Builder
	var totalTokens int

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		typ, _ := ev["type"].(string)
		switch typ {
		case "response.output_text.delta":
			if d, ok := ev["delta"].(string); ok {
				text.WriteString(d)
			}
		case "response.output_text.done":
			// If no deltas arrived, fall back to the full text field.
			if text.Len() == 0 {
				if t, ok := ev["text"].(string); ok {
					text.WriteString(t)
				}
			}
		case "response.completed":
			totalTokens = extractTotalTokens(ev)
		case "response.failed", "response.incomplete", "error":
			return text.String(), totalTokens, errors.New(codexEventErrorMessage(ev))
		}
	}
	if err := scanner.Err(); err != nil {
		return text.String(), totalTokens, fmt.Errorf("codex: read stream: %w", err)
	}
	return text.String(), totalTokens, nil
}

func extractTotalTokens(ev map[string]any) int {
	resp, _ := ev["response"].(map[string]any)
	if resp == nil {
		return 0
	}
	usage, _ := resp["usage"].(map[string]any)
	if usage == nil {
		return 0
	}
	input := numField(usage, "input_tokens", "prompt_tokens")
	output := numField(usage, "output_tokens", "completion_tokens")
	if total := numField(usage, "total_tokens"); total > 0 {
		return total
	}
	return input + output
}

func numField(m map[string]any, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			}
		}
	}
	return 0
}

func codexEventErrorMessage(ev map[string]any) string {
	if e, ok := ev["error"].(map[string]any); ok {
		if msg, ok := e["message"].(string); ok && msg != "" {
			return "Codex stream error: " + msg
		}
	}
	if resp, ok := ev["response"].(map[string]any); ok {
		if e, ok := resp["error"].(map[string]any); ok {
			if msg, ok := e["message"].(string); ok && msg != "" {
				return "Codex stream error: " + msg
			}
		}
		if status, ok := resp["status"].(string); ok && status != "" {
			return "Codex stream error: status=" + status
		}
	}
	if typ, ok := ev["type"].(string); ok {
		return "Codex stream error: " + typ
	}
	return "Codex stream error"
}

// ---- credential resolution (mirrors tradex/agent/models.ts) ----

func resolveCodexCredentials() (accessToken, accountID string, err error) {
	if key := os.Getenv("CODEX_API_KEY"); key != "" {
		return key, os.Getenv("CODEX_ACCOUNT_ID"), nil
	}
	authPath := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
	if os.Getenv("CODEX_HOME") == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", "", fmt.Errorf("codex: resolve home dir: %w", herr)
		}
		authPath = filepath.Join(home, ".codex", "auth.json")
	}
	raw, rerr := os.ReadFile(authPath)
	if rerr != nil {
		// No auth file is not fatal here — surface empty token; caller errors out.
		return "", "", nil
	}
	var parsed map[string]any
	if jerr := json.Unmarshal(raw, &parsed); jerr != nil {
		return "", "", nil
	}
	tokens, _ := parsed["tokens"].(map[string]any)
	accessToken = firstString(tokens, "access_token")
	if accessToken == "" {
		accessToken = firstString(parsed, "access_token", "accessToken")
	}
	if accessToken == "" {
		return "", "", nil
	}
	if accessTokenIsExpired(accessToken) {
		return "", "", errors.New("Codex CLI access token is expired. Run `codex` once to refresh the login.")
	}
	accountID = firstString(tokens, "account_id")
	if accountID == "" {
		accountID = firstString(parsed, "account_id", "accountId")
	}
	if accountID == "" {
		accountID = accountIDFromToken(accessToken)
	}
	return accessToken, accountID, nil
}

func firstString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func accountIDFromToken(token string) string {
	claims := jwtClaims(token)
	if id, ok := claims["chatgpt_account_id"].(string); ok && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	if nested, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := nested["chatgpt_account_id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func accessTokenIsExpired(token string) bool {
	claims := jwtClaims(token)
	if exp, ok := claims["exp"].(float64); ok {
		return int64(exp) <= time.Now().Unix()
	}
	return false
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	payload := parts[1]
	if pad := len(payload) % 4; pad != 0 {
		payload += strings.Repeat("=", 4-pad)
	}
	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		// Try raw (no padding) URL encoding as a fallback.
		decoded, err = base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return map[string]any{}
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return map[string]any{}
	}
	return claims
}
