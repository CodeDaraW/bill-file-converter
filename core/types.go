package core

import (
	"context"
	"encoding/json"
	"io"

	"github.com/deb-sig/bill-file-converter/core/adapters"
)

type Input struct {
	Path     string
	Reader   io.Reader
	FileName string
	MIMEType string
	Files    []InputFile
}

type InputFile struct {
	Path     string
	Reader   io.Reader
	FileName string
	MIMEType string
}

type Options struct {
	MinerU          MinerUClient
	AdapterKey      string
	OutputDir       string
	AdapterRegistry AdapterRegistry
	SkipCSV         bool
	LogWriter       io.Writer
	taskID          string
	processLog      *processLogger
	auditWriter     *auditWriter
}

type AdapterRegistry interface {
	MustGet(key string) (adapters.Adapter, error)
}

type Document struct {
	Metadata map[string]string `json:"metadata,omitempty"`
	Title    string            `json:"title,omitempty"`
	Tables   []Table           `json:"tables"`
}

type SourceInfo struct {
	Path     string           `json:"path,omitempty"`
	FileName string           `json:"file_name,omitempty"`
	MIMEType string           `json:"mime_type,omitempty"`
	Files    []SourceFileInfo `json:"files,omitempty"`
}

type SourceFileInfo struct {
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
	JSONPath        string `json:"json_path,omitempty"`
	CSVPath         string `json:"csv_path,omitempty"`
	LogPath         string `json:"log_path,omitempty"`
	AuditDir        string `json:"audit_dir,omitempty"`
	ContentListPath string `json:"content_list_path,omitempty"`
	CSVBytes        []byte `json:"-"`
	JSONBytes       []byte `json:"-"`
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

type MinerUContent struct {
	Type      string         `json:"type,omitempty"`
	Text      string         `json:"text,omitempty"`
	TableBody string         `json:"table_body,omitempty"`
	PageIndex *int           `json:"page_idx,omitempty"`
	Raw       map[string]any `json:"-"`
}

func (c MinerUContent) MarshalJSON() ([]byte, error) {
	payload := map[string]any{}
	for key, value := range c.Raw {
		payload[key] = value
	}
	if c.Type != "" {
		payload["type"] = c.Type
	}
	if c.Text != "" {
		payload["text"] = c.Text
	}
	if c.TableBody != "" {
		payload["table_body"] = c.TableBody
	}
	if c.PageIndex != nil {
		payload["page_idx"] = *c.PageIndex
	}
	return json.Marshal(payload)
}

type MinerUParseResult struct {
	ContentList []MinerUContent
	RawRequest  string
	RawResponse string
}

type MinerUClient interface {
	Parse(ctx context.Context, input Input) (MinerUParseResult, error)
}

// Pinger is implemented by clients that can verify connectivity and credentials.
type Pinger interface {
	Ping(ctx context.Context) error
}
