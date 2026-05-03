package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/deb-sig/bill-file-converter/core"
)

type GeminiProvider struct {
	httpProvider
}

func NewGeminiProvider(config ProviderConfig) GeminiProvider {
	if config.BaseURL == "" {
		config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	return GeminiProvider{httpProvider: newHTTPProvider(config)}
}

func (p GeminiProvider) Ping(ctx context.Context) error {
	endpoint := joinURL(p.config.BaseURL, "/models")
	if key := p.apiKey(); key != "" {
		endpoint = addQuery(endpoint, url.Values{"key": []string{key}})
	}
	return p.ping(ctx, endpoint, nil)
}

func (p GeminiProvider) Generate(ctx context.Context, req core.VLMRequest) (core.VLMResponse, error) {
	parts := []map[string]any{{"text": req.Prompt}}
	for _, image := range req.Images {
		encoded, err := imageBase64(image.Path)
		if err != nil {
			return core.VLMResponse{}, err
		}
		mimeType := image.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": mimeType,
				"data":      encoded,
			},
		})
	}
	body := map[string]any{
		"contents": []map[string]any{{"role": "user", "parts": parts}},
		"generationConfig": map[string]any{
			"temperature": req.Temperature,
		},
	}
	if p.config.ThinkingEnabled {
		budget := p.config.ThinkingBudgetTokens
		if budget <= 0 {
			budget = 1024
		}
		body["generationConfig"].(map[string]any)["thinkingConfig"] = map[string]any{
			"thinkingBudget": budget,
		}
	}
	for key, value := range p.config.Extra {
		body[key] = value
	}
	endpoint := joinURL(p.config.BaseURL, fmt.Sprintf("/models/%s:generateContent", p.config.Model))
	if key := p.apiKey(); key != "" {
		endpoint = addQuery(endpoint, url.Values{"key": []string{key}})
	}
	rawRequest := rawProviderRequest("POST", endpoint, body)
	data, err := p.doJSON(ctx, "POST", endpoint, body, nil)
	if err != nil {
		return core.VLMResponse{RawRequest: rawRequest, RawResponse: string(data), Raw: string(data)}, err
	}
	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return core.VLMResponse{RawRequest: rawRequest, RawResponse: string(data), Raw: string(data)}, err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return core.VLMResponse{RawRequest: rawRequest, RawResponse: string(data), Raw: string(data)}, fmt.Errorf("provider returned no candidates")
	}
	return core.VLMResponse{
		Text:        parsed.Candidates[0].Content.Parts[0].Text,
		Raw:         string(data),
		RawRequest:  rawRequest,
		RawResponse: string(data),
	}, nil
}
