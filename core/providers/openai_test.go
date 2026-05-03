package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deb-sig/bill-file-converter/core"
)

func TestOpenAICompatibleProviderPayload(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "page.png")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"metadata\":{},\"tables\":[]}"}}]}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "vlm",
	})
	resp, err := provider.Generate(context.Background(), core.VLMRequest{
		Prompt: "extract",
		Images: []core.PageImage{{Path: imagePath, MIMEType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text == "" {
		t.Fatal("empty response text")
	}
	if !strings.Contains(resp.RawRequest, `/chat/completions`) || !strings.Contains(resp.RawResponse, `"choices"`) {
		t.Fatalf("missing raw audit data: %#v", resp)
	}
	if received["model"] != "vlm" {
		t.Fatalf("unexpected model payload: %#v", received)
	}
	messages := received["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected text + image content, got %#v", content)
	}
	if _, ok := received["reasoning_effort"]; ok {
		t.Fatalf("thinking should be disabled by default: %#v", received)
	}
}

func TestOpenAICompatibleProviderReturnsRawAuditDataOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL: server.URL,
		Model:   "vlm",
	})
	resp, err := provider.Generate(context.Background(), core.VLMRequest{Prompt: "extract"})
	if err == nil {
		t.Fatal("expected provider error")
	}
	if !strings.Contains(resp.RawRequest, `/chat/completions`) {
		t.Fatalf("missing raw request on provider error: %#v", resp)
	}
	if !strings.Contains(resp.RawResponse, `"bad request"`) {
		t.Fatalf("missing raw response on provider error: %#v", resp)
	}
}

func TestOpenAICompatibleProviderThinkingPayload(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "page.png")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL:              server.URL,
		Model:                "vlm",
		ThinkingEnabled:      true,
		ThinkingBudgetTokens: 2048,
	})
	if _, err := provider.Generate(context.Background(), core.VLMRequest{
		Prompt: "extract",
		Images: []core.PageImage{{Path: imagePath, MIMEType: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	if received["reasoning_effort"] != "medium" {
		t.Fatalf("missing reasoning_effort: %#v", received)
	}
	thinking := received["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"].(float64) != 2048 {
		t.Fatalf("unexpected thinking payload: %#v", thinking)
	}
}

func TestOpenAICompatibleProviderRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"temporary"}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL:    server.URL,
		Model:      "vlm",
		MaxRetries: 3,
	})
	if _, err := provider.Generate(context.Background(), core.VLMRequest{Prompt: "extract"}); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (2 retries), got %d", got)
	}
}

func TestOpenAICompatibleProviderPing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Fatalf("unexpected ping request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer ping-key" {
			t.Fatalf("expected ping to send auth header, got %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL: server.URL,
		APIKey:  "ping-key",
	})
	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("ping should succeed: %v", err)
	}
}

func TestOpenAICompatibleProviderPingPropagatesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{BaseURL: server.URL})
	if err := provider.Ping(context.Background()); err == nil {
		t.Fatal("expected ping to surface 401")
	}
}

func TestOpenAICompatibleProviderDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer server.Close()

	provider := NewOpenAICompatibleProvider(ProviderConfig{
		BaseURL:    server.URL,
		Model:      "vlm",
		MaxRetries: 5,
	})
	if _, err := provider.Generate(context.Background(), core.VLMRequest{Prompt: "extract"}); err == nil {
		t.Fatal("expected provider error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected client error to not retry, got %d attempt(s)", got)
	}
}
