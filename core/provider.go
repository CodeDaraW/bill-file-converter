package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ProviderConfig struct {
	Provider             string            `json:"provider"`
	BaseURL              string            `json:"base_url"`
	APIKey               string            `json:"api_key"`
	APIKeyEnv            string            `json:"api_key_env"`
	Model                string            `json:"model"`
	Headers              map[string]string `json:"headers"`
	Timeout              time.Duration     `json:"-"`
	MaxRetries           int               `json:"max_retries"`
	Temperature          float64           `json:"temperature"`
	ThinkingEnabled      bool              `json:"thinking_enabled"`
	ThinkingBudgetTokens int               `json:"thinking_budget_tokens"`
	Extra                map[string]any    `json:"extra"`
}

func (c ProviderConfig) MarshalJSON() ([]byte, error) {
	type providerConfigJSON struct {
		Provider             string            `json:"provider"`
		BaseURL              string            `json:"base_url"`
		APIKey               string            `json:"api_key"`
		APIKeyEnv            string            `json:"api_key_env"`
		Model                string            `json:"model"`
		Headers              map[string]string `json:"headers,omitempty"`
		Timeout              string            `json:"timeout,omitempty"`
		MaxRetries           int               `json:"max_retries"`
		Temperature          float64           `json:"temperature"`
		ThinkingEnabled      bool              `json:"thinking_enabled"`
		ThinkingBudgetTokens int               `json:"thinking_budget_tokens,omitempty"`
		Extra                map[string]any    `json:"extra,omitempty"`
	}
	timeout := ""
	if c.Timeout != 0 {
		timeout = c.Timeout.String()
	}
	return json.Marshal(providerConfigJSON{
		Provider:             c.Provider,
		BaseURL:              c.BaseURL,
		APIKey:               c.APIKey,
		APIKeyEnv:            c.APIKeyEnv,
		Model:                c.Model,
		Headers:              c.Headers,
		Timeout:              timeout,
		MaxRetries:           c.MaxRetries,
		Temperature:          c.Temperature,
		ThinkingEnabled:      c.ThinkingEnabled,
		ThinkingBudgetTokens: c.ThinkingBudgetTokens,
		Extra:                c.Extra,
	})
}

func (c *ProviderConfig) UnmarshalJSON(data []byte) error {
	type providerConfigJSON struct {
		Provider             string            `json:"provider"`
		BaseURL              string            `json:"base_url"`
		APIKey               string            `json:"api_key"`
		APIKeyEnv            string            `json:"api_key_env"`
		Model                string            `json:"model"`
		Headers              map[string]string `json:"headers"`
		Timeout              json.RawMessage   `json:"timeout"`
		MaxRetries           int               `json:"max_retries"`
		Temperature          float64           `json:"temperature"`
		ThinkingEnabled      bool              `json:"thinking_enabled"`
		ThinkingBudgetTokens int               `json:"thinking_budget_tokens"`
		Extra                map[string]any    `json:"extra"`
	}
	var decoded providerConfigJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = ProviderConfig{
		Provider:             decoded.Provider,
		BaseURL:              decoded.BaseURL,
		APIKey:               decoded.APIKey,
		APIKeyEnv:            decoded.APIKeyEnv,
		Model:                decoded.Model,
		Headers:              decoded.Headers,
		MaxRetries:           decoded.MaxRetries,
		Temperature:          decoded.Temperature,
		ThinkingEnabled:      decoded.ThinkingEnabled,
		ThinkingBudgetTokens: decoded.ThinkingBudgetTokens,
		Extra:                decoded.Extra,
	}
	if len(decoded.Timeout) == 0 || string(decoded.Timeout) == "null" {
		return nil
	}
	var timeoutString string
	if err := json.Unmarshal(decoded.Timeout, &timeoutString); err == nil {
		timeout, parseErr := time.ParseDuration(timeoutString)
		if parseErr != nil {
			return parseErr
		}
		c.Timeout = timeout
		return nil
	}
	var timeoutNanos int64
	if err := json.Unmarshal(decoded.Timeout, &timeoutNanos); err != nil {
		return err
	}
	c.Timeout = time.Duration(timeoutNanos)
	return nil
}

func NewProvider(config ProviderConfig) (VLMProvider, error) {
	switch strings.ToLower(config.Provider) {
	case "", "openai", "openai-compatible", "lmstudio", "ollama":
		return NewOpenAICompatibleProvider(config), nil
	case "anthropic":
		return NewAnthropicProvider(config), nil
	case "gemini", "google":
		return NewGeminiProvider(config), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", config.Provider)
	}
}

type httpProvider struct {
	config ProviderConfig
	client *http.Client
}

func newHTTPProvider(config ProviderConfig) httpProvider {
	timeout := config.Timeout
	return httpProvider{
		config: config,
		client: &http.Client{Timeout: timeout},
	}
}

func (p httpProvider) apiKey() string {
	if p.config.APIKey != "" {
		return p.config.APIKey
	}
	if p.config.APIKeyEnv != "" {
		return os.Getenv(p.config.APIKeyEnv)
	}
	return ""
}

func (p httpProvider) doJSON(ctx context.Context, method, endpoint string, body any, headers map[string]string) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range p.config.Headers {
		req.Header.Set(key, value)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func imageDataURL(path, mimeType string) (string, error) {
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(path))
	}
	if mimeType == "" {
		mimeType = "image/png"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func imageBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func joinURL(base, suffix string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return suffix
	}
	if strings.HasPrefix(suffix, "http://") || strings.HasPrefix(suffix, "https://") {
		return suffix
	}
	return base + "/" + strings.TrimLeft(suffix, "/")
}

func addQuery(raw string, values url.Values) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for key, vals := range values {
		for _, value := range vals {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
