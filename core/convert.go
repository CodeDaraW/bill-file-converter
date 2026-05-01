package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Convert(ctx context.Context, input Input, options Options) (Result, error) {
	if options.TaskID == "" {
		taskID, err := newTaskID(time.Now())
		if err != nil {
			return Result{}, err
		}
		options.TaskID = taskID
	}
	baseOutputDir := options.OutputDir
	if baseOutputDir == "" {
		baseOutputDir = "."
	}
	options.OutputDir = filepath.Join(baseOutputDir, options.TaskID)
	logf(options, "checking input")
	if err := validatePDFInput(input); err != nil {
		return Result{}, err
	}
	adapter, err := MustGetAdapter(options.AdapterKey)
	if err != nil {
		return Result{}, err
	}
	logf(options, "using adapter %s (%s)", adapter.Key, adapter.Name)
	if options.Provider == nil {
		return Result{}, fmt.Errorf("missing VLM provider")
	}
	if options.Renderer == nil {
		options.Renderer = NewExternalRenderer()
	}
	if err := os.MkdirAll(options.OutputDir, 0o755); err != nil {
		return Result{}, err
	}
	logf(options, "checking PDF renderer")
	if err := options.Renderer.Check(ctx); err != nil {
		return Result{}, err
	}

	imageDir := filepath.Join(options.OutputDir, "pages")
	logf(options, "rendering PDF pages to %s", imageDir)
	images, err := options.Renderer.Render(ctx, input, imageDir)
	if err != nil {
		return Result{}, err
	}
	logf(options, "rendered %d page image(s); sending to VLM", len(images))

	response, err := options.Provider.Generate(ctx, VLMRequest{
		Prompt:      adapter.Prompt,
		Images:      images,
		Temperature: options.Temperature,
	})
	if err != nil {
		return Result{}, err
	}
	logf(options, "received VLM response; parsing JSON")

	var doc Document
	if err := json.Unmarshal([]byte(extractJSON(response.Text)), &doc); err != nil {
		return Result{}, fmt.Errorf("parse model json: %w", err)
	}
	logf(options, "validating extracted document")
	report := ValidateDocument(doc, adapter)
	var csvBytes []byte
	if !options.SkipCSV {
		logf(options, "exporting CSV")
		var csvErr error
		csvBytes, csvErr = ExportCSV(doc)
		if csvErr != nil {
			report.Errors = append(report.Errors, csvErr.Error())
		}
	} else {
		logf(options, "skipping CSV export")
	}

	result := Result{
		TaskID:           options.TaskID,
		AdapterKey:       adapter.Key,
		AdapterName:      adapter.Name,
		Source:           sourceInfo(input),
		GeneratedAt:      time.Now().Format(time.RFC3339),
		Metadata:         doc.Metadata,
		Tables:           doc.Tables,
		ValidationReport: report,
		Artifacts: Artifacts{
			PageImages:  images,
			CSVBytes:    csvBytes,
			RawResponse: response.Raw,
		},
	}

	logf(options, "writing artifacts to %s", options.OutputDir)
	if err := writeArtifacts(options.OutputDir, &result, options.SaveDebugArtifacts); err != nil {
		return Result{}, err
	}
	if report.HasErrors() {
		logf(options, "validation failed with %d error(s)", len(report.Errors))
		for _, validationErr := range report.Errors {
			logf(options, "validation error: %s", validationErr)
		}
		return result, ValidationError{Report: report}
	}
	logf(options, "done")
	return result, nil
}

type ValidationError struct {
	Report ValidationReport
}

func (e ValidationError) Error() string {
	return "validation failed"
}

func validatePDFInput(input Input) error {
	name := input.FileName
	if name == "" {
		name = input.Path
	}
	if input.MIMEType != "" && input.MIMEType != "application/pdf" {
		return fmt.Errorf("unsupported input MIME type %q: v1 only supports PDF; print/export emails as PDF first", input.MIMEType)
	}
	if name != "" && strings.ToLower(filepath.Ext(name)) != ".pdf" {
		return fmt.Errorf("unsupported input file %q: v1 only supports PDF; print/export emails as PDF first", name)
	}
	if input.Path == "" && input.Reader == nil {
		return fmt.Errorf("missing input PDF")
	}
	return nil
}

func sourceInfo(input Input) SourceInfo {
	name := input.FileName
	if name == "" && input.Path != "" {
		name = filepath.Base(input.Path)
	}
	return SourceInfo{
		Path:     input.Path,
		FileName: name,
		MIMEType: input.MIMEType,
	}
}

func writeArtifacts(outputDir string, result *Result, saveDebug bool) error {
	jsonPath := filepath.Join(outputDir, "result.json")
	result.Artifacts.JSONPath = jsonPath

	if len(result.Artifacts.CSVBytes) > 0 {
		csvPath := filepath.Join(outputDir, "result.csv")
		result.Artifacts.CSVPath = csvPath
	}

	if saveDebug && result.Artifacts.RawResponse != "" {
		result.Artifacts.RawPath = filepath.Join(outputDir, "raw_response.txt")
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	result.Artifacts.JSONBytes = jsonBytes
	if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
		return err
	}

	if result.Artifacts.CSVPath != "" {
		if err := os.WriteFile(result.Artifacts.CSVPath, result.Artifacts.CSVBytes, 0o644); err != nil {
			return err
		}
	}

	if result.Artifacts.RawPath != "" {
		if err := os.WriteFile(result.Artifacts.RawPath, []byte(result.Artifacts.RawResponse), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func extractJSON(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		text = strings.TrimSuffix(text, "```")
	}
	return strings.TrimSpace(text)
}

func logf(options Options, format string, args ...any) {
	if options.LogWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(options.LogWriter, "%s [bill-file-converter] [%s] "+format+"\n", append([]any{time.Now().Format(time.RFC3339), options.TaskID}, args...)...)
}

func newTaskID(now time.Time) (string, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate task id: %w", err)
	}
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(random[:]), nil
}
