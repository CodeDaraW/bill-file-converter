package cli

import (
	"encoding/json"
	"os"

	"github.com/deb-sig/bill-file-converter/core/providers"
)

type Config struct {
	Provider providers.ProviderConfig `json:"provider"`
	Renderer struct {
		Command string `json:"command"`
		DPI     int    `json:"dpi"`
	} `json:"renderer"`
	Conversion struct {
		MaxConcurrency int `json:"max_concurrency"`
	} `json:"conversion"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func DefaultConfig() Config {
	config := Config{}
	config.Provider = providers.ProviderConfig{
		Provider:        "openai-compatible",
		BaseURL:         "http://localhost:1234/v1",
		APIKeyEnv:       "LLM_API_KEY",
		Model:           "qwen3-vl-32b-instruct",
		Temperature:     0,
		ThinkingEnabled: false,
	}
	config.Renderer.Command = "pdftoppm"
	config.Renderer.DPI = 200
	config.Conversion.MaxConcurrency = 4
	return config
}

func WriteDefaultConfig(path string) error {
	config := DefaultConfig()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
