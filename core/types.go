package core

import (
	"context"
	"io"
)

type Input struct {
	Path     string
	Reader   io.Reader
	FileName string
	MIMEType string
}

type Options struct {
	Provider           VLMProvider
	Renderer           Renderer
	AdapterKey         string
	OutputDir          string
	SaveDebugArtifacts bool
	SkipCSV            bool
	LogWriter          io.Writer
	Temperature        float64
	TaskID             string
}

type PageImage struct {
	Page     int    `json:"page"`
	Path     string `json:"path"`
	MIMEType string `json:"mime_type"`
}

type Document struct {
	Metadata map[string]string `json:"metadata,omitempty"`
	Title    string            `json:"title,omitempty"`
	Tables   []Table           `json:"tables"`
}

type SourceInfo struct {
	Path     string `json:"path,omitempty"`
	FileName string `json:"file_name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

type Table struct {
	Name        string      `json:"name,omitempty"`
	Headers     []string    `json:"headers"`
	Rows        [][]*string `json:"rows"`
	SourcePages []int       `json:"source_pages,omitempty"`
	Warnings    []string    `json:"warnings,omitempty"`
}

type ValidationReport struct {
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func (r ValidationReport) HasErrors() bool {
	return len(r.Errors) > 0
}

type Artifacts struct {
	PageImages  []PageImage `json:"page_images,omitempty"`
	JSONPath    string      `json:"json_path,omitempty"`
	CSVPath     string      `json:"csv_path,omitempty"`
	RawPath     string      `json:"raw_path,omitempty"`
	CSVBytes    []byte      `json:"-"`
	JSONBytes   []byte      `json:"-"`
	RawResponse string      `json:"-"`
}

type Result struct {
	TaskID           string            `json:"task_id"`
	AdapterKey       string            `json:"adapter_key"`
	AdapterName      string            `json:"adapter_name"`
	Source           SourceInfo        `json:"source"`
	GeneratedAt      string            `json:"generated_at"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Tables           []Table           `json:"tables"`
	ValidationReport ValidationReport  `json:"validation_report"`
	Artifacts        Artifacts         `json:"artifacts,omitempty"`
}

type Renderer interface {
	Render(ctx context.Context, input Input, outputDir string) ([]PageImage, error)
	Check(ctx context.Context) error
}

type VLMRequest struct {
	Prompt      string
	Images      []PageImage
	Temperature float64
	Extra       map[string]any
}

type VLMResponse struct {
	Text string
	Raw  string
}

type VLMProvider interface {
	Generate(ctx context.Context, req VLMRequest) (VLMResponse, error)
}
