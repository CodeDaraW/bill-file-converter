package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
	resp, err := provider.Generate(context.Background(), VLMRequest{
		Prompt: "extract",
		Images: []PageImage{{Path: imagePath, MIMEType: "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text == "" {
		t.Fatal("empty response text")
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
	if _, err := provider.Generate(context.Background(), VLMRequest{
		Prompt: "extract",
		Images: []PageImage{{Path: imagePath, MIMEType: "image/png"}},
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
