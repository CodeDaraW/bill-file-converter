package cli

import (
	"encoding/json"
	"os"

	"github.com/deb-sig/bill-file-converter/core"
)

type Config struct {
	Provider core.ProviderConfig `json:"provider"`
	Renderer struct {
		Command string `json:"command"`
		DPI     int    `json:"dpi"`
	} `json:"renderer"`
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
	config.Provider = core.ProviderConfig{
		Provider:        "openai-compatible",
		BaseURL:         "http://localhost:1234/v1",
		APIKeyEnv:       "LLM_API_KEY",
		Model:           "qwen/qwen3.5-vl-9b",
		Temperature:     0,
		ThinkingEnabled: false,
	}
	config.Renderer.Command = "pdftoppm"
	config.Renderer.DPI = 200
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
