package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/deb-sig/bill-file-converter/core/adapters"
	tasklogger "github.com/deb-sig/bill-file-converter/core/logger"
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
	for _, dir := range []string{resultDir, options.OutputDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Result{}, err
		}
	}
	runLogger, err := tasklogger.NewTaskLogger(options.OutputDir, options.taskID, options.AdapterKey, options.LogWriter)
	if err != nil {
		return Result{}, err
	}
	defer runLogger.Close()
	runLogger.Infof("checking input")
	if err := validatePDFInput(input); err != nil {
		runLogger.Errorf("input validation failed: %s", err)
		runLogger.SaveFailure(sourceInfo(input), "", err)
		return Result{}, err
	}
	registry := options.AdapterRegistry
	if registry == nil {
		err := fmt.Errorf("missing adapter registry")
		runLogger.Errorf("adapter registry missing")
		runLogger.SaveFailure(sourceInfo(input), "", err)
		return Result{}, err
	}
	adapter, err := registry.MustGet(options.AdapterKey)
	if err != nil {
		runLogger.Errorf("adapter lookup failed: %s", err)
		runLogger.SaveFailure(sourceInfo(input), "", err)
		return Result{}, err
	}
	runLogger.Infof("using adapter %s (%s)", adapter.Key, adapter.Name)
	if options.MinerU == nil {
		err := fmt.Errorf("missing MinerU client")
		runLogger.Errorf("MinerU client missing")
		runLogger.SaveFailure(sourceInfo(input), adapter.Name, err)
		return Result{}, err
	}

	parseInput := input
	if adapter.RemoveImages {
		runLogger.Infof("removing images from PDF input(s) before MinerU parsing")
		var preprocessErr error
		parseInput, preprocessErr = removeInputPDFImages(ctx, input, filepath.Join(runLogger.Dir(), "preprocessed"), runLogger)
		if preprocessErr != nil {
			runLogger.Errorf("PDF image removal failed: %s", preprocessErr)
			runLogger.SaveFailure(sourceInfo(input), adapter.Name, preprocessErr)
			return Result{}, preprocessErr
		}
	}

	runLogger.Infof("submitting %d PDF input(s) to MinerU", len(inputFiles(parseInput)))
	parseResult, err := parseMinerUInInputOrder(ctx, options.MinerU, parseInput)
	rawRequestPath, rawResponsePath := runLogger.SaveRawPayloads(parseResult.RawRequest, parseResult.RawResponse)
	if err != nil {
		runLogger.Errorf("MinerU parse failed: %s", err)
		runLogger.SaveFailure(sourceInfo(input), adapter.Name, err)
		return Result{}, err
	}
	contentListPath := filepath.Join(runLogger.Dir(), "content_list.json")
	if err := writeContentList(contentListPath, parseResult.ContentList); err != nil {
		runLogger.Errorf("writing content_list.json failed: %s", err)
		runLogger.SaveFailure(sourceInfo(input), adapter.Name, err)
		return Result{}, err
	}

	runLogger.Infof("cleaning MinerU content list")
	doc := DocumentFromMinerUContent(parseResult.ContentList, adapter)
	runLogger.Infof("validating extracted document")
	report := ValidateDocument(doc, adapter)
	var csvBytes []byte
	if !options.SkipCSV {
		runLogger.Infof("exporting CSV")
		var csvErr error
		csvBytes, csvErr = ExportCSV(doc)
		if csvErr != nil {
			report.Errors = append(report.Errors, csvErr.Error())
			runLogger.Errorf("CSV export failed: %s", csvErr)
		}
	} else {
		runLogger.Infof("skipping CSV export")
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
			CSVBytes:              csvBytes,
			LoggerDir:             runLogger.Dir(),
			ProcessLogPath:        runLogger.Path(),
			ContentListPath:       contentListPath,
			MinerURawRequestPath:  rawRequestPath,
			MinerURawResponsePath: rawResponsePath,
		},
	}

	runLogger.Infof("writing artifacts to %s", options.OutputDir)
	if err := writeArtifacts(resultDir, &result); err != nil {
		runLogger.Errorf("writing result artifacts failed: %s", err)
		result.Artifacts.FailurePath = runLogger.SaveFailure(sourceInfo(input), adapter.Name, err)
		return Result{}, err
	}
	if report.HasErrors() {
		runLogger.Errorf("validation failed with %d error(s)", len(report.Errors))
		for _, validationErr := range report.Errors {
			runLogger.Errorf("validation error: %s", validationErr)
		}
		result.Artifacts.FailurePath = runLogger.SaveFailure(sourceInfo(input), adapter.Name, ValidationError{Report: report})
		return result, ValidationError{Report: report}
	}
	runLogger.Infof("done")
	return result, nil
}

func parseMinerUInInputOrder(ctx context.Context, client MinerUClient, input Input) (MinerUParseResult, error) {
	files := inputFiles(input)
	if len(files) <= 1 {
		return client.Parse(ctx, input)
	}
	var combined MinerUParseResult
	var rawRequests []json.RawMessage
	var rawResponses []json.RawMessage
	pageOffset := 0
	for _, file := range files {
		result, err := client.Parse(ctx, Input{
			Path:     file.Path,
			Reader:   file.Reader,
			FileName: inputFileName(file),
			MIMEType: file.MIMEType,
		})
		appendRawJSON := func(values []json.RawMessage, raw string) []json.RawMessage {
			if strings.TrimSpace(raw) == "" {
				return values
			}
			return append(values, json.RawMessage(raw))
		}
		rawRequests = appendRawJSON(rawRequests, result.RawRequest)
		rawResponses = appendRawJSON(rawResponses, result.RawResponse)
		if err != nil {
			combined.RawRequest = marshalRawMessages(rawRequests)
			combined.RawResponse = marshalRawMessages(rawResponses)
			return combined, err
		}
		maxPage := -1
		for _, item := range result.ContentList {
			if item.PageIndex != nil {
				adjusted := *item.PageIndex + pageOffset
				item.PageIndex = &adjusted
				if adjusted > maxPage {
					maxPage = adjusted
				}
			}
			combined.ContentList = append(combined.ContentList, item)
		}
		if maxPage >= pageOffset {
			pageOffset = maxPage + 1
		} else {
			pageOffset++
		}
	}
	combined.RawRequest = marshalRawMessages(rawRequests)
	combined.RawResponse = marshalRawMessages(rawResponses)
	return combined, nil
}

func marshalRawMessages(values []json.RawMessage) string {
	if len(values) == 0 {
		return ""
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func DocumentFromMinerUContent(contentList []MinerUContent, adapter adapters.Adapter) Document {
	doc := Document{
		Metadata: map[string]string{},
	}
	rawText := dedupeRawText(contentList)
	if rawText != "" {
		doc.Metadata["raw_text"] = rawText
	}

	for _, item := range contentList {
		if strings.ToLower(item.Type) != "table" || strings.TrimSpace(item.TableBody) == "" {
			continue
		}
		rows, err := parseHTMLTable(item.TableBody)
		if err != nil {
			doc.Tables = append(doc.Tables, Table{Warnings: []string{fmt.Sprintf("parse table html: %s", err)}})
			continue
		}
		table, ok := tableFromRows(rows, adapter, pageFromContent(item))
		if !ok {
			continue
		}
		doc = mergeCleanedTable(doc, table)
	}
	return doc
}

func dedupeRawText(contentList []MinerUContent) string {
	seen := map[string]bool{}
	values := []string{}
	for _, item := range contentList {
		if strings.ToLower(item.Type) == "table" {
			continue
		}
		text := normalizeText(item.Text)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		values = append(values, text)
	}
	return strings.Join(values, "\n")
}

func tableFromRows(rows [][]string, adapter adapters.Adapter, page int) (Table, bool) {
	headerIndex, headers := matchingHeaderRow(rows, adapter)
	if headerIndex < 0 {
		return Table{}, false
	}
	table := Table{
		Headers:     headers,
		SourcePages: appendUniqueInts(nil, page),
	}
	for _, row := range rows[headerIndex+1:] {
		if stringRowIsEmpty(row) || stringRowMatchesHeaders(row, headers) || rowMatchesHeaderAlias(row, adapter) || rowStartsWithHeaderAlias(row, adapter) {
			continue
		}
		normalized := normalizeRowWidth(row, len(headers))
		if !rowMatchesGuards(normalized, adapter) {
			continue
		}
		cells := make([]*string, len(normalized))
		for i, value := range normalized {
			value = normalizeText(value)
			if value == "" {
				continue
			}
			cells[i] = &value
		}
		table.Rows = append(table.Rows, cells)
	}
	if len(table.Rows) == 0 {
		return Table{}, false
	}
	return table, true
}

func matchingHeaderRow(rows [][]string, adapter adapters.Adapter) (int, []string) {
	for rowIndex, row := range rows {
		normalized := normalizeStringRow(row)
		if equalStringSlices(normalized, adapter.Headers) || equalHeaderSlices(normalized, adapter.Headers) {
			return rowIndex, append([]string(nil), adapter.Headers...)
		}
		for _, alias := range adapter.HeaderAliases {
			if equalStringSlices(normalized, alias) || equalHeaderSlices(normalized, alias) {
				return rowIndex, append([]string(nil), adapter.Headers...)
			}
		}
	}
	return -1, nil
}

func normalizeStringRow(row []string) []string {
	normalized := make([]string, len(row))
	for i, value := range row {
		normalized[i] = normalizeText(value)
	}
	return normalized
}

func normalizeRowWidth(row []string, width int) []string {
	normalized := normalizeStringRow(row)
	if len(normalized) > width {
		return normalized[:width]
	}
	for len(normalized) < width {
		normalized = append(normalized, "")
	}
	return normalized
}

func stringRowMatchesHeaders(row []string, headers []string) bool {
	normalized := normalizeStringRow(row)
	return equalStringSlices(normalized, headers) || equalHeaderSlices(normalized, headers)
}

func rowMatchesHeaderAlias(row []string, adapter adapters.Adapter) bool {
	normalized := normalizeStringRow(row)
	for _, alias := range adapter.HeaderAliases {
		if equalStringSlices(normalized, alias) || equalHeaderSlices(normalized, alias) || rowMatchesHeaderAliasPrefix(normalized, alias) {
			return true
		}
	}
	return false
}

func rowStartsWithHeaderAlias(row []string, adapter adapters.Adapter) bool {
	if len(row) == 0 {
		return false
	}
	first := normalizeText(row[0])
	if first == "" {
		return false
	}
	if first == normalizeText(adapter.Headers[0]) {
		return true
	}
	for _, alias := range adapter.HeaderAliases {
		if len(alias) > 0 && first == normalizeText(alias[0]) {
			return true
		}
	}
	return false
}

func rowMatchesHeaderAliasPrefix(row []string, alias []string) bool {
	if len(row) != len(alias) {
		return false
	}
	matches := 0
	for i := range alias {
		cell := normalizeText(row[i])
		header := normalizeText(alias[i])
		if cell == "" {
			continue
		}
		if cell == header || strings.HasPrefix(header, cell) {
			matches++
			continue
		}
		return false
	}
	return matches >= 3
}

func rowMatchesGuards(row []string, adapter adapters.Adapter) bool {
	for _, guard := range adapter.RowGuards {
		if guard.Column < 0 || guard.Column >= len(row) {
			return false
		}
		if !valueMatchesGuardFormat(normalizeText(row[guard.Column]), guard.Format) {
			return false
		}
	}
	return true
}

func valueMatchesGuardFormat(value string, format adapters.RowGuardFormat) bool {
	switch format {
	case adapters.RowGuardFormatYYYYDashMMDashDD:
		parsed, err := time.Parse("2006-01-02", value)
		return err == nil && parsed.Format("2006-01-02") == value
	case adapters.RowGuardFormatYYYYMMDD:
		parsed, err := time.Parse("20060102", value)
		return err == nil && parsed.Format("20060102") == value
	case adapters.RowGuardFormatYYYYMMDDHHMMSS:
		parsed, err := time.Parse("2006010215:04:05", value)
		return err == nil && parsed.Format("2006010215:04:05") == value
	case adapters.RowGuardFormatMMSlashDD:
		parsed, err := time.Parse("2006/01/02", "2000/"+value)
		return err == nil && parsed.Format("01/02") == value
	default:
		return false
	}
}

func stringRowIsEmpty(row []string) bool {
	for _, value := range row {
		if normalizeText(value) != "" {
			return false
		}
	}
	return true
}

func mergeCleanedTable(doc Document, table Table) Document {
	targetIndex := matchingTableIndex(doc.Tables, table)
	if targetIndex < 0 {
		doc.Tables = append(doc.Tables, table)
		return doc
	}
	target := &doc.Tables[targetIndex]
	target.Rows = append(target.Rows, table.Rows...)
	target.SourcePages = appendUniqueInts(target.SourcePages, table.SourcePages...)
	target.Warnings = append(target.Warnings, table.Warnings...)
	return doc
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
		if normalizeText(a[i]) != normalizeText(b[i]) {
			return false
		}
	}
	return true
}

func equalHeaderSlices(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if normalizeHeaderText(a[i]) != normalizeHeaderText(b[i]) {
			return false
		}
	}
	return true
}

func normalizeHeaderText(value string) string {
	return strings.ReplaceAll(normalizeText(value), " ", "")
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

func pageFromContent(item MinerUContent) int {
	if item.PageIndex == nil {
		return 0
	}
	return *item.PageIndex + 1
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

type ValidationError struct {
	Report ValidationReport
}

func (e ValidationError) Error() string {
	return "validation failed"
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

func removeInputPDFImages(ctx context.Context, input Input, outputDir string, runLogger *tasklogger.Logger) (Input, error) {
	files := inputFiles(input)
	if len(files) == 0 {
		return Input{}, fmt.Errorf("missing input PDF")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return Input{}, err
	}
	processed := make([]InputFile, 0, len(files))
	for index, file := range files {
		outPath := filepath.Join(outputDir, fmt.Sprintf("input-%03d.pdf", index+1))
		if err := removePDFImages(ctx, file, outPath); err != nil {
			return Input{}, fmt.Errorf("remove images from input %d (%s): %w", index+1, inputFileName(file), err)
		}
		runLogger.Verbosef("wrote image-free PDF for %s to %s", inputFileName(file), outPath)
		processed = append(processed, InputFile{
			Path:     outPath,
			FileName: inputFileName(file),
			MIMEType: "application/pdf",
		})
	}
	if len(processed) == 1 {
		return Input{Path: processed[0].Path, FileName: processed[0].FileName, MIMEType: processed[0].MIMEType}, nil
	}
	return Input{Files: processed}, nil
}

func removePDFImages(ctx context.Context, file InputFile, outputPath string) error {
	gsPath, err := exec.LookPath("gs")
	if err != nil {
		return fmt.Errorf("Ghostscript executable \"gs\" not found; install ghostscript to use this profile")
	}
	inputPath, cleanup, err := materializePDFInput(file, filepath.Dir(outputPath))
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, gsPath,
		"-q",
		"-dNOPAUSE",
		"-dBATCH",
		"-sDEVICE=pdfwrite",
		"-dFILTERIMAGE",
		"-sOutputFile="+outputPath,
		inputPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("run ghostscript: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func materializePDFInput(file InputFile, outputDir string) (string, func(), error) {
	if file.Path != "" {
		return file.Path, func() {}, nil
	}
	tmp, err := os.CreateTemp(outputDir, "source-*.pdf")
	if err != nil {
		return "", nil, err
	}
	if _, err := io.Copy(tmp, file.Reader); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", nil, err
	}
	return tmp.Name(), func() { _ = os.Remove(tmp.Name()) }, nil
}

func inputFileName(file InputFile) string {
	if file.FileName != "" {
		return file.FileName
	}
	if file.Path != "" {
		return filepath.Base(file.Path)
	}
	return "input.pdf"
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
		return fmt.Errorf("unsupported input MIME type %q: only PDF input is supported", file.MIMEType)
	}
	if name != "" && strings.ToLower(filepath.Ext(name)) != ".pdf" {
		return fmt.Errorf("unsupported input file %q: only PDF input is supported", name)
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

func writeContentList(path string, contentList []MinerUContent) error {
	data, err := json.MarshalIndent(contentList, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
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

func newTaskID(t time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", t.Format("20060102-150405"), hex.EncodeToString(suffix[:])), nil
}
