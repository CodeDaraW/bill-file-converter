// Prompt safety:
// 本文件中的 continuationPrompt 拼接会随源码一起公开发布。除了运行时由 seed 阶段
// 注入的表头/表名等结构信息，prompt 字面量本身严禁嵌入任何来自真实账单的数据
// （账号、姓名、申请时间、验证码、电子流水号、统计周期、PDF 文件名等），即使是
// 部分星号脱敏的形式。示例值必须明显是占位符。完整规则参见 README.md 的
// "Prompt 编写规则"。

package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/deb-sig/bill-file-converter/core/adapters"
)

func Convert(ctx context.Context, input Input, options Options) (Result, error) {
	taskID, err := newTaskID(time.Now())
	if err != nil {
		return Result{}, err
	}
	options.taskID = taskID
	baseOutputDir := options.OutputDir
	if baseOutputDir == "" {
		baseOutputDir = "."
	}
	options.OutputDir = filepath.Join(baseOutputDir, options.taskID)
	resultDir := filepath.Join(options.OutputDir, "result")
	intermediateDir := filepath.Join(options.OutputDir, "intermediate")
	auditDir := filepath.Join(intermediateDir, "audit")
	imageDir := filepath.Join(intermediateDir, "pages")
	for _, dir := range []string{resultDir, intermediateDir, imageDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, err
		}
	}
	auditWriter, err := newAuditWriter(auditDir)
	if err != nil {
		return Result{}, err
	}
	options.auditWriter = auditWriter
	processLog, err := newProcessLogger(filepath.Join(intermediateDir, "bill_file_converter.log"), options.taskID)
	if err != nil {
		return Result{}, err
	}
	defer processLog.Close()
	options.processLog = processLog
	logInfof(options, "checking input")
	if err := validatePDFInput(input); err != nil {
		logErrorf(options, "input validation failed: %s", err)
		logFailure(options, input, "", err)
		return Result{}, err
	}
	registry := options.AdapterRegistry
	if registry == nil {
		err := fmt.Errorf("missing adapter registry")
		logErrorf(options, "adapter registry missing")
		logFailure(options, input, "", err)
		return Result{}, err
	}
	adapter, err := registry.MustGet(options.AdapterKey)
	if err != nil {
		logErrorf(options, "adapter lookup failed: %s", err)
		logFailure(options, input, "", err)
		return Result{}, err
	}
	logInfof(options, "using adapter %s (%s)", adapter.Key, adapter.Name)
	if options.Provider == nil {
		err := fmt.Errorf("missing VLM provider")
		logErrorf(options, "VLM provider missing")
		logFailure(options, input, adapter.Name, err)
		return Result{}, err
	}
	if options.Renderer == nil {
		options.Renderer = NewExternalRenderer()
	}
	logInfof(options, "checking PDF renderer")
	if err := options.Renderer.Check(ctx); err != nil {
		logErrorf(options, "PDF renderer check failed: %s", err)
		logFailure(options, input, adapter.Name, err)
		return Result{}, err
	}

	logInfof(options, "rendering PDF pages to %s", imageDir)
	images, err := renderInputPages(ctx, input, options, imageDir)
	if err != nil {
		logErrorf(options, "PDF rendering failed: %s", err)
		logFailure(options, input, adapter.Name, err)
		return Result{}, err
	}
	logInfof(options, "rendered %d page image(s); starting VLM parsing", len(images))
	doc, err := parseDocumentPages(ctx, input, options, adapter, images)
	if err != nil {
		logFailure(options, input, adapter.Name, err)
		return Result{}, err
	}
	logInfof(options, "validating extracted document")
	report := ValidateDocument(doc, adapter)
	var csvBytes []byte
	if !options.SkipCSV {
		logInfof(options, "exporting CSV")
		var csvErr error
		csvBytes, csvErr = ExportCSV(doc)
		if csvErr != nil {
			report.Errors = append(report.Errors, csvErr.Error())
			logErrorf(options, "CSV export failed: %s", csvErr)
		}
	} else {
		logInfof(options, "skipping CSV export")
	}

	result := Result{
		TaskID:           options.taskID,
		AdapterKey:       adapter.Key,
		AdapterName:      adapter.Name,
		Source:           sourceInfo(input),
		GeneratedAt:      time.Now().Format(time.RFC3339),
		Metadata:         doc.Metadata,
		Tables:           doc.Tables,
		ValidationReport: report,
		Artifacts: Artifacts{
			PageImages: images,
			CSVBytes:   csvBytes,
			LogPath:    filepath.Join(intermediateDir, "bill_file_converter.log"),
			AuditDir:   auditDir,
		},
	}

	logInfof(options, "writing artifacts to %s", options.OutputDir)
	if err := writeArtifacts(resultDir, &result); err != nil {
		logErrorf(options, "writing result artifacts failed: %s", err)
		logFailure(options, input, adapter.Name, err)
		return Result{}, err
	}
	if report.HasErrors() {
		logErrorf(options, "validation failed with %d error(s)", len(report.Errors))
		for _, validationErr := range report.Errors {
			logErrorf(options, "validation error: %s", validationErr)
		}
		logFailure(options, input, adapter.Name, ValidationError{Report: report})
		return result, ValidationError{Report: report}
	}
	logInfof(options, "done")
	return result, nil
}

func parseDocumentPages(ctx context.Context, input Input, options Options, adapter adapters.Adapter, images []PageImage) (Document, error) {
	if len(images) == 0 {
		return Document{}, fmt.Errorf("PDF renderer produced no page images")
	}
	if adapter.SeedPages <= 0 {
		logInfof(options, "adapter seed parsing disabled; parsing %d page image(s) independently", len(images))
		return parseIndependentPages(ctx, input, options, adapter, images)
	}

	seedCount := adapter.SeedPages
	if seedCount > len(images) {
		seedCount = len(images)
	}
	seedImages := images[:seedCount]
	logInfof(options, "parsing first %d page(s) for metadata and table structure", seedCount)
	firstDoc, err := parseSingleDocument(ctx, options, "seed_pages", adapter.Prompt, seedImages)
	if err != nil {
		return Document{}, err
	}
	if seedCount == len(images) {
		return normalizeDocumentRows(firstDoc), nil
	}
	if len(firstDoc.Tables) == 0 {
		logWarningf(options, "seed pages produced no tables; skipping remaining page parsing because table structure is unknown")
		return normalizeDocumentRows(firstDoc), nil
	}

	remaining := images[seedCount:]
	concurrency := boundedConcurrency(options.MaxConcurrency, len(remaining))
	logInfof(options, "parsing %d remaining page(s) with concurrency %d", len(remaining), concurrency)

	pageDocs, err := runPageWorkers(ctx, remaining, concurrency, func(ctx context.Context, image PageImage) (Document, error) {
		prompt, promptErr := continuationPrompt(adapter, firstDoc, image.Page)
		if promptErr != nil {
			return Document{}, promptErr
		}
		logInfof(options, "parsing continuation page %d", image.Page)
		doc, parseErr := parseSingleDocument(ctx, options, fmt.Sprintf("page_%d", image.Page), prompt, []PageImage{image})
		if parseErr != nil {
			logErrorf(options, "continuation page %d failed: %s", image.Page, parseErr)
		}
		return doc, parseErr
	})
	if err != nil {
		return Document{}, err
	}

	merged := normalizeDocumentRows(firstDoc)
	for i, pageDoc := range pageDocs {
		merged = mergeContinuationDocument(merged, normalizeDocumentRows(pageDoc), remaining[i].Page)
	}
	return merged, nil
}

func parseIndependentPages(ctx context.Context, input Input, options Options, adapter adapters.Adapter, images []PageImage) (Document, error) {
	concurrency := boundedConcurrency(options.MaxConcurrency, len(images))
	logInfof(options, "parsing %d page(s) independently with concurrency %d", len(images), concurrency)

	pageDocs, err := runPageWorkers(ctx, images, concurrency, func(ctx context.Context, image PageImage) (Document, error) {
		logInfof(options, "parsing independent page %d", image.Page)
		doc, parseErr := parseSingleDocument(ctx, options, fmt.Sprintf("page_%d", image.Page), adapter.Prompt, []PageImage{image})
		if parseErr != nil {
			logErrorf(options, "independent page %d failed: %s", image.Page, parseErr)
		}
		return doc, parseErr
	})
	if err != nil {
		return Document{}, err
	}

	merged := Document{}
	for i, pageDoc := range pageDocs {
		merged = mergeIndependentDocument(merged, normalizeDocumentRows(pageDoc), images[i].Page)
	}
	return merged, nil
}

const defaultPageConcurrency = 4
const maxPageConcurrency = 32

func boundedConcurrency(requested, jobs int) int {
	concurrency := requested
	if concurrency <= 0 {
		concurrency = defaultPageConcurrency
	}
	if concurrency > maxPageConcurrency {
		concurrency = maxPageConcurrency
	}
	if concurrency > jobs {
		concurrency = jobs
	}
	return concurrency
}

func runPageWorkers(ctx context.Context, images []PageImage, concurrency int, work func(context.Context, PageImage) (Document, error)) ([]Document, error) {
	if len(images) == 0 {
		return nil, nil
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	results := make([]Document, len(images))
	jobs := make(chan int)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		firstErr   error
		firstErrMu sync.Mutex
		wg         sync.WaitGroup
	)
	recordErr := func(err error) {
		firstErrMu.Lock()
		defer firstErrMu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if workerCtx.Err() != nil {
					continue
				}
				doc, err := work(workerCtx, images[index])
				if err != nil {
					recordErr(err)
					continue
				}
				results[index] = doc
			}
		}()
	}

	for index := range images {
		select {
		case <-workerCtx.Done():
			break
		case jobs <- index:
		}
		if workerCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func mergeIndependentDocument(base Document, pageDoc Document, page int) Document {
	if len(base.Metadata) == 0 && len(pageDoc.Metadata) > 0 {
		base.Metadata = pageDoc.Metadata
	}
	if base.Title == "" && pageDoc.Title != "" {
		base.Title = pageDoc.Title
	}
	for _, pageTable := range pageDoc.Tables {
		if len(pageTable.Rows) == 0 {
			continue
		}
		targetIndex := matchingTableIndex(base.Tables, pageTable)
		if targetIndex < 0 {
			if len(pageTable.SourcePages) == 0 {
				pageTable.SourcePages = append(pageTable.SourcePages, page)
			}
			base.Tables = append(base.Tables, pageTable)
			continue
		}
		target := &base.Tables[targetIndex]
		target.Rows = append(target.Rows, pageTable.Rows...)
		target.SourcePages = appendUniqueInts(target.SourcePages, pageTable.SourcePages...)
		if len(pageTable.SourcePages) == 0 {
			target.SourcePages = appendUniqueInts(target.SourcePages, page)
		}
		target.Warnings = append(target.Warnings, pageTable.Warnings...)
	}
	return base
}

func parseSingleDocument(ctx context.Context, options Options, auditLabel string, prompt string, images []PageImage) (Document, error) {
	response, err := options.Provider.Generate(ctx, VLMRequest{
		Prompt:      prompt,
		Images:      images,
		Temperature: options.Temperature,
	})
	logProviderAudit(options, auditLabel, response)
	if err != nil {
		logErrorf(options, "%s VLM request failed: %s", auditLabel, err)
		return Document{}, err
	}
	logInfof(options, "%s VLM response received; parsing JSON", auditLabel)

	var doc Document
	if err := json.Unmarshal([]byte(extractJSON(response.Text)), &doc); err != nil {
		parseErr := fmt.Errorf("parse %s model json: %w; see bill_file_converter.log for raw request and response", auditLabel, err)
		logErrorf(options, "%s model JSON parse failed: %s", auditLabel, parseErr)
		return Document{}, parseErr
	}
	doc = overrideSourcePages(doc, images)
	return doc, nil
}

func overrideSourcePages(doc Document, images []PageImage) Document {
	pages := make([]int, 0, len(images))
	for _, image := range images {
		if image.Page != 0 {
			pages = append(pages, image.Page)
		}
	}
	if len(pages) == 0 {
		return doc
	}
	for tableIndex := range doc.Tables {
		doc.Tables[tableIndex].SourcePages = append([]int(nil), pages...)
	}
	return doc
}

func continuationPrompt(adapter adapters.Adapter, seed Document, page int) (string, error) {
	tables := make([]map[string]any, 0, len(seed.Tables))
	for _, table := range seed.Tables {
		if len(table.Headers) == 0 {
			continue
		}
		tables = append(tables, map[string]any{
			"name":    table.Name,
			"headers": table.Headers,
		})
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("first page produced no usable table headers")
	}
	data, err := json.MarshalIndent(tables, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`你正在继续解析同一份 %s（%s）的第 %d 页。

前置 seed 页面已经确认了表格结构。当前页只允许按下面这些已确认表格结构抽取数据，不要重新发明表头、列名或表格格式：
%s

当前页解析规则：
- 只使用当前页图片中的视觉证据。
- 只抽取当前页中属于上述表格结构的数据行。
- 如果当前页重复出现表头，headers 仍按已确认结构输出，但 rows 中不要包含表头行。
- 如果当前页没有重复表头，也必须按已确认 headers 的列顺序输出 rows。
- 每行单元格数量必须和对应 headers 数量一致。
- 只使用同一视觉行范围内的文字，不要把上一行或下一行的文字填入当前行。
- 空单元格必须输出 null。null 是合法结果，不需要补齐。
- 没有明确视觉证据时不要推断。
- 金额字段保留原始符号、小数位、分隔符和币种文字。
- 不要输出 CSV。
- 不要输出 metadata；metadata 已由第一页解析确定。
- 只输出 JSON，结构必须匹配：
{
  "tables": [
    {
      "name": "表格名称",
      "headers": ["必须和已确认 headers 完全一致"],
      "rows": [["单元格文字或 null"]],
      "source_pages": [%d],
      "warnings": ["可选警告"]
    }
  ]
}
`, adapter.Name, adapter.Key, page, string(data), page), nil
}

func normalizeDocumentRows(doc Document) Document {
	for tableIndex := range doc.Tables {
		table := &doc.Tables[tableIndex]
		rows := table.Rows[:0]
		for _, row := range table.Rows {
			if rowMatchesHeaders(row, table.Headers) || rowIsEmpty(row) {
				continue
			}
			rows = append(rows, row)
		}
		table.Rows = rows
	}
	return doc
}

func rowMatchesHeaders(row []*string, headers []string) bool {
	if len(row) != len(headers) {
		return false
	}
	for i, cell := range row {
		if cell == nil || strings.TrimSpace(*cell) != strings.TrimSpace(headers[i]) {
			return false
		}
	}
	return true
}

func rowIsEmpty(row []*string) bool {
	for _, cell := range row {
		if cell != nil && strings.TrimSpace(*cell) != "" {
			return false
		}
	}
	return true
}

func mergeContinuationDocument(base Document, pageDoc Document, page int) Document {
	for _, pageTable := range pageDoc.Tables {
		if len(pageTable.Rows) == 0 {
			continue
		}
		targetIndex := matchingTableIndex(base.Tables, pageTable)
		if targetIndex < 0 {
			pageTable.Warnings = append(pageTable.Warnings, fmt.Sprintf("page %d table did not match first-page headers", page))
			base.Tables = append(base.Tables, pageTable)
			continue
		}
		target := &base.Tables[targetIndex]
		target.Rows = append(target.Rows, pageTable.Rows...)
		target.SourcePages = appendUniqueInts(target.SourcePages, pageTable.SourcePages...)
		if len(pageTable.SourcePages) == 0 {
			target.SourcePages = appendUniqueInts(target.SourcePages, page)
		}
		target.Warnings = append(target.Warnings, pageTable.Warnings...)
	}
	return base
}

func matchingTableIndex(tables []Table, candidate Table) int {
	for i, table := range tables {
		if equalStringSlices(table.Headers, candidate.Headers) {
			return i
		}
	}
	return -1
}

func equalStringSlices(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func appendUniqueInts(base []int, values ...int) []int {
	seen := make(map[int]bool, len(base)+len(values))
	for _, value := range base {
		seen[value] = true
	}
	for _, value := range values {
		if value == 0 || seen[value] {
			continue
		}
		base = append(base, value)
		seen[value] = true
	}
	sort.Ints(base)
	return base
}

type ValidationError struct {
	Report ValidationReport
}

func (e ValidationError) Error() string {
	return "validation failed"
}

func renderInputPages(ctx context.Context, input Input, options Options, imageDir string) ([]PageImage, error) {
	files := inputFiles(input)
	if len(files) == 1 {
		images, err := options.Renderer.Render(ctx, inputFromFile(files[0]), imageDir)
		if err != nil {
			return nil, err
		}
		return renumberPageImages(images, 1), nil
	}

	images := []PageImage{}
	nextPage := 1
	for index, file := range files {
		fileDir := filepath.Join(imageDir, fmt.Sprintf("input-%03d", index+1))
		logInfof(options, "rendering input PDF %d/%d: %s", index+1, len(files), inputFileName(file))
		rendered, err := options.Renderer.Render(ctx, inputFromFile(file), fileDir)
		if err != nil {
			return nil, fmt.Errorf("render input %d (%s): %w", index+1, inputFileName(file), err)
		}
		rendered = renumberPageImages(rendered, nextPage)
		nextPage += len(rendered)
		images = append(images, rendered...)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("renderer produced no page images")
	}
	return images, nil
}

func inputFiles(input Input) []InputFile {
	if len(input.Files) > 0 {
		return input.Files
	}
	return []InputFile{{
		Path:     input.Path,
		Reader:   input.Reader,
		FileName: input.FileName,
		MIMEType: input.MIMEType,
	}}
}

func inputFromFile(file InputFile) Input {
	return Input{
		Path:     file.Path,
		Reader:   file.Reader,
		FileName: file.FileName,
		MIMEType: file.MIMEType,
	}
}

func inputFileName(file InputFile) string {
	if file.FileName != "" {
		return file.FileName
	}
	if file.Path != "" {
		return filepath.Base(file.Path)
	}
	return "<reader>"
}

func renumberPageImages(images []PageImage, startPage int) []PageImage {
	for i := range images {
		images[i].Page = startPage + i
	}
	return images
}

func validatePDFInput(input Input) error {
	files := inputFiles(input)
	if len(files) == 0 {
		return fmt.Errorf("missing input PDF")
	}
	for index, file := range files {
		if err := validatePDFInputFile(file); err != nil {
			if len(files) == 1 {
				return err
			}
			return fmt.Errorf("input %d: %w", index+1, err)
		}
	}
	return nil
}

func validatePDFInputFile(file InputFile) error {
	name := file.FileName
	if name == "" {
		name = file.Path
	}
	if file.MIMEType != "" && file.MIMEType != "application/pdf" {
		return fmt.Errorf("unsupported input MIME type %q: v1 only supports PDF; print/export emails as PDF first", file.MIMEType)
	}
	if name != "" && strings.ToLower(filepath.Ext(name)) != ".pdf" {
		return fmt.Errorf("unsupported input file %q: v1 only supports PDF; print/export emails as PDF first", name)
	}
	if file.Path == "" && file.Reader == nil {
		return fmt.Errorf("missing input PDF")
	}
	return nil
}

func sourceInfo(input Input) SourceInfo {
	files := inputFiles(input)
	if len(files) > 1 {
		source := SourceInfo{Files: make([]SourceFileInfo, 0, len(files))}
		for _, file := range files {
			source.Files = append(source.Files, sourceFileInfo(file))
		}
		return source
	}
	if len(files) == 1 {
		fileInfo := sourceFileInfo(files[0])
		return SourceInfo{Path: fileInfo.Path, FileName: fileInfo.FileName, MIMEType: fileInfo.MIMEType}
	}
	return SourceInfo{}
}

func sourceFileInfo(input InputFile) SourceFileInfo {
	name := input.FileName
	if name == "" && input.Path != "" {
		name = filepath.Base(input.Path)
	}
	return SourceFileInfo{
		Path:     input.Path,
		FileName: name,
		MIMEType: input.MIMEType,
	}
}

func writeArtifacts(resultDir string, result *Result) error {
	jsonPath := filepath.Join(resultDir, "result.json")
	result.Artifacts.JSONPath = jsonPath

	if len(result.Artifacts.CSVBytes) > 0 {
		csvPath := filepath.Join(resultDir, "result.csv")
		result.Artifacts.CSVPath = csvPath
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

	return nil
}

func rawResponseText(response VLMResponse) string {
	if response.RawResponse != "" {
		return response.RawResponse
	}
	return response.Raw
}

func logProviderAudit(options Options, label string, response VLMResponse) {
	if options.auditWriter == nil {
		return
	}
	if response.RawRequest != "" {
		path, err := options.auditWriter.writeRaw(label+"_request.json", response.RawRequest)
		if err != nil {
			logErrorf(options, "failed to persist %s request: %s", label, err)
		} else if path != "" {
			logVerbosef(options, "wrote %s raw request to %s", label, path)
		}
	}
	if rawText := rawResponseText(response); rawText != "" {
		path, err := options.auditWriter.writeRaw(label+"_response.json", rawText)
		if err != nil {
			logErrorf(options, "failed to persist %s response: %s", label, err)
		} else if path != "" {
			logVerbosef(options, "wrote %s raw response to %s", label, path)
		}
	}
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

func logVerbosef(options Options, format string, args ...any) {
	logWithLevel(options, LogVerbose, format, args...)
}

func logInfof(options Options, format string, args ...any) {
	logWithLevel(options, LogInfo, format, args...)
}

func logWarningf(options Options, format string, args ...any) {
	logWithLevel(options, LogWarning, format, args...)
}

func logErrorf(options Options, format string, args ...any) {
	logWithLevel(options, LogError, format, args...)
}

func logWithLevel(options Options, level LogLevel, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s [%s] [%s] %s", time.Now().Format(time.RFC3339), strings.ToUpper(string(normalizeLogLevel(level))), options.taskID, message)
	if options.processLog != nil {
		if written := options.processLog.Write(level, message); written != "" {
			line = written
		}
	}
	if options.LogWriter == nil {
		return
	}
	_, _ = fmt.Fprintf(options.LogWriter, "%s\n", colorizeStdLogLine(level, line))
}

func newTaskID(now time.Time) (string, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate task id: %w", err)
	}
	return now.Format("20060102-150405") + "-" + hex.EncodeToString(random[:]), nil
}

func colorizeStdLogLine(level LogLevel, line string) string {
	switch normalizeLogLevel(level) {
	case LogWarning:
		return "\033[33m" + line + "\033[0m"
	case LogError:
		return "\033[31m" + line + "\033[0m"
	default:
		return line
	}
}
