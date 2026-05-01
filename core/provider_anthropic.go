package core

import (
	"context"
	"encoding/json"
	"fmt"
)

type AnthropicProvider struct {
	httpProvider
}

func NewAnthropicProvider(config ProviderConfig) AnthropicProvider {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.anthropic.com/v1"
	}
	return AnthropicProvider{httpProvider: newHTTPProvider(config)}
}

func (p AnthropicProvider) Generate(ctx context.Context, req VLMRequest) (VLMResponse, error) {
	content := []map[string]any{{"type": "text", "text": req.Prompt}}
	for _, image := range req.Images {
		encoded, err := imageBase64(image.Path)
		if err != nil {
			return VLMResponse{}, err
		}
		mimeType := image.MIMEType
		if mimeType == "" {
			mimeType = "image/png"
		}
		content = append(content, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mimeType,
				"data":       encoded,
			},
		})
	}
	body := map[string]any{
		"model":       p.config.Model,
		"max_tokens":  8192,
		"messages":    []map[string]any{{"role": "user", "content": content}},
		"temperature": req.Temperature,
	}
	if p.config.ThinkingEnabled {
		budget := p.config.ThinkingBudgetTokens
		if budget <= 0 {
			budget = 1024
		}
		body["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": budget,
		}
	}
	for key, value := range p.config.Extra {
		body[key] = value
	}
	headers := map[string]string{"anthropic-version": "2023-06-01"}
	if key := p.apiKey(); key != "" {
		headers["x-api-key"] = key
	}
	data, err := p.doJSON(ctx, "POST", joinURL(p.config.BaseURL, "/messages"), body, headers)
	if err != nil {
		return VLMResponse{}, err
	}
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return VLMResponse{}, err
	}
	for _, item := range parsed.Content {
		if item.Type == "text" && item.Text != "" {
			return VLMResponse{Text: item.Text, Raw: string(data)}, nil
		}
	}
	return VLMResponse{}, fmt.Errorf("provider returned no text content")
}
