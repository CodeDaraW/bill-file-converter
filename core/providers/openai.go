package providers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/deb-sig/bill-file-converter/core"
)

type OpenAICompatibleProvider struct {
	httpProvider
}

func NewOpenAICompatibleProvider(config ProviderConfig) OpenAICompatibleProvider {
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	return OpenAICompatibleProvider{httpProvider: newHTTPProvider(config)}
}

func (p OpenAICompatibleProvider) Ping(ctx context.Context) error {
	headers := map[string]string{}
	if key := p.apiKey(); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	return p.ping(ctx, joinURL(p.config.BaseURL, "/models"), headers)
}

func (p OpenAICompatibleProvider) Generate(ctx context.Context, req core.VLMRequest) (core.VLMResponse, error) {
	content := []map[string]any{{"type": "text", "text": req.Prompt}}
	for _, image := range req.Images {
		dataURL, err := imageDataURL(image.Path, image.MIMEType)
		if err != nil {
			return core.VLMResponse{}, err
		}
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": dataURL,
			},
		})
	}
	body := map[string]any{
		"model": p.config.Model,
		"messages": []map[string]any{
			{"role": "user", "content": content},
		},
		"temperature": req.Temperature,
	}
	if p.config.ThinkingEnabled {
		body["reasoning_effort"] = "medium"
		if p.config.ThinkingBudgetTokens > 0 {
			body["thinking"] = map[string]any{
				"type":          "enabled",
				"budget_tokens": p.config.ThinkingBudgetTokens,
			}
		}
	}
	for key, value := range p.config.Extra {
		body[key] = value
	}
	headers := map[string]string{}
	if key := p.apiKey(); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	endpoint := joinURL(p.config.BaseURL, "/chat/completions")
	rawRequest := rawProviderRequest("POST", endpoint, body)
	data, err := p.doJSON(ctx, "POST", endpoint, body, headers)
	if err != nil {
		return core.VLMResponse{RawRequest: rawRequest, RawResponse: string(data), Raw: string(data)}, err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return core.VLMResponse{RawRequest: rawRequest, RawResponse: string(data), Raw: string(data)}, err
	}
	if len(parsed.Choices) == 0 {
		return core.VLMResponse{RawRequest: rawRequest, RawResponse: string(data), Raw: string(data)}, fmt.Errorf("provider returned no choices")
	}
	return core.VLMResponse{
		Text:        parsed.Choices[0].Message.Content,
		Raw:         string(data),
		RawRequest:  rawRequest,
		RawResponse: string(data),
	}, nil
}
