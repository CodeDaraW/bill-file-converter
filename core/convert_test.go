package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/deb-sig/bill-file-converter/core/adapters"
)

var taskIDPattern = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{8}$`)

type fakeRenderer struct {
	images []PageImage
	err    error
}

func (r fakeRenderer) Check(context.Context) error { return r.err }

func (r fakeRenderer) Render(context.Context, Input, string) ([]PageImage, error) {
	return r.images, r.err
}

type recordingRenderer struct {
	mu     sync.Mutex
	inputs []Input
}

func (r *recordingRenderer) Check(context.Context) error { return nil }

func (r *recordingRenderer) Render(_ context.Context, input Input, outputDir string) ([]PageImage, error) {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	index := len(r.inputs)
	r.mu.Unlock()
	return []PageImage{{Page: 1, Path: filepath.Join(outputDir, fmt.Sprintf("page-%d.png", index)), MIMEType: "image/png"}}, nil
}

type fakeProvider struct {
	text string
	req  VLMRequest
}

func (p *fakeProvider) Generate(_ context.Context, req VLMRequest) (VLMResponse, error) {
	p.req = req
	return VLMResponse{Text: p.text, Raw: `{"raw":true}`, RawRequest: `{"request":true}`, RawResponse: `{"response":true}`}, nil
}

type scriptedProvider struct {
	mu        sync.Mutex
	responses []string
	requests  []VLMRequest
}

func (p *scriptedProvider) Generate(_ context.Context, req VLMRequest) (VLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := len(p.requests)
	p.requests = append(p.requests, req)
	if index >= len(p.responses) {
		return VLMResponse{}, nil
	}
	return VLMResponse{
		Text:        p.responses[index],
		Raw:         fmt.Sprintf(`{"index":%d}`, index),
		RawRequest:  fmt.Sprintf(`{"request_index":%d}`, index),
		RawResponse: fmt.Sprintf(`{"response_index":%d}`, index),
	}, nil
}

type pageAwareProvider struct {
	mu        sync.Mutex
	responses map[int]string
	requests  []VLMRequest
}

func (p *pageAwareProvider) Generate(_ context.Context, req VLMRequest) (VLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	page := 0
	if len(req.Images) > 0 {
		page = req.Images[0].Page
	}
	text := p.responses[page]
	return VLMResponse{
		Text:        text,
		Raw:         fmt.Sprintf(`{"page":%d}`, page),
		RawRequest:  fmt.Sprintf(`{"request_page":%d}`, page),
		RawResponse: fmt.Sprintf(`{"response_page":%d}`, page),
	}, nil
}

func builtinRegistry() AdapterRegistry {
	return adapters.BuiltinRegistry()
}

func TestConvertWritesJSONAndCSV(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	imagePath := filepath.Join(dir, "page-1.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{text: `{
		"metadata": {
			"title": "招商银行交易流水",
			"english_title": "Transaction Statement of China Merchants Bank",
			"statement_period": "2024/01/01-2024/01/31",
			"name": "张三",
			"account_no": "6222 **** 1234",
			"account_type": "ALL/全币种",
			"sub_branch": "北京支行",
			"application_time": "2024-02-01 12:00",
			"verification_code": "9T0000"
		},
		"title": "招商银行交易流水",
		"tables": [{
			"name": "交易明细",
			"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息", "客户摘要"],
			"rows": [["2024-01-01", "CNY", null, "¥1,234.00", "商户,含逗号", "对手方", "客户摘要"]],
			"source_pages": [1],
			"warnings": ["人工复核空值"]
		}]
	}`}
	result, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:        provider,
		Renderer:        fakeRenderer{images: []PageImage{{Page: 1, Path: imagePath, MIMEType: "image/png"}}},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       dir,
		LogWriter:       &logs,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.Metadata["account_no"] != "6222 **** 1234" {
		t.Fatalf("metadata not preserved: %#v", result.Metadata)
	}
	if !taskIDPattern.MatchString(result.TaskID) {
		t.Fatalf("task id format unexpected: %q", result.TaskID)
	}
	if result.Source.Path != "bill.pdf" || result.Source.FileName != "bill.pdf" {
		t.Fatalf("source info not populated: %#v", result.Source)
	}
	if result.GeneratedAt == "" {
		t.Fatal("generated_at not populated")
	}
	if provider.req.Prompt == "" || len(provider.req.Images) != 1 {
		t.Fatalf("provider request not populated: %#v", provider.req)
	}
	taskDir := filepath.Join(dir, result.TaskID)
	csvData, err := os.ReadFile(filepath.Join(taskDir, "result", "result.csv"))
	if err != nil {
		t.Fatal(err)
	}
	csvText := string(csvData)
	if strings.Contains(csvText, "招商银行交易流水") {
		t.Fatalf("csv should not contain title/metadata: %q", csvText)
	}
	if !strings.HasPrefix(csvText, "记账日期,货币,交易金额,联机余额,交易摘要,对手信息,客户摘要\n") {
		t.Fatalf("csv should start with table header only: %q", csvText)
	}
	if !strings.Contains(csvText, `2024-01-01,CNY,,"¥1,234.00","商户,含逗号",对手方,客户摘要`) {
		t.Fatalf("csv did not escape comma/null as expected: %q", csvText)
	}
	jsonData, err := os.ReadFile(filepath.Join(taskDir, "result", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonData), `"metadata"`) {
		t.Fatalf("json missing metadata: %s", jsonData)
	}
	logData, err := os.ReadFile(filepath.Join(taskDir, "intermediate", "bill_file_converter.log"))
	if err != nil {
		t.Fatalf("expected bill_file_converter.log: %v", err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "[INFO]") {
		t.Fatalf("expected INFO log level, got %q", string(logData))
	}
	if strings.Contains(logText, "BEGIN") || strings.Contains(logText, "END") {
		t.Fatalf("log should no longer contain inline JSON blocks, got %q", logText)
	}
	auditDir := filepath.Join(taskDir, "intermediate", "audit")
	for _, name := range []string{"page_1_request.json", "page_1_response.json"} {
		if _, err := os.Stat(filepath.Join(auditDir, name)); err != nil {
			t.Fatalf("expected audit file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(auditDir, "failure.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no failure.json on success, stat err=%v", err)
	}
	if !strings.Contains(logs.String(), "["+result.TaskID+"]") || !strings.Contains(logs.String(), "T") || !strings.Contains(logs.String(), "rendering PDF pages") || !strings.Contains(logs.String(), "done") {
		t.Fatalf("expected progress logs, got %q", logs.String())
	}
}

func TestConvertParsesFirstPageThenRemainingPages(t *testing.T) {
	dir := t.TempDir()
	seedAdapter, err := adapters.BuiltinRegistry().MustGet("cmb_debit")
	if err != nil {
		t.Fatal(err)
	}
	seedAdapter.SeedPages = 1
	provider := &pageAwareProvider{responses: map[int]string{
		1: `{
			"metadata": {
				"title": "招商银行交易流水",
				"english_title": "Transaction Statement of China Merchants Bank",
				"statement_period": "2024/01/01-2024/01/31",
				"name": "张三",
				"account_no": "6222 **** 1234",
				"account_type": "ALL/全币种",
				"sub_branch": "北京支行",
				"application_time": "2024-02-01 12:00",
				"verification_code": "9T0000"
			},
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [["2024-01-01", "CNY", "1.00", "10.00", "第一页", "对手1"]],
				"source_pages": [1]
			}]
		}`,
		2: `{
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [
					["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
					["2024-01-02", "CNY", "2.00", "12.00", "第二页", "对手2"]
				],
				"source_pages": [2]
			}]
		}`,
		3: `{
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [["2024-01-03", "CNY", "3.00", "15.00", "第三页", "对手3"]],
				"source_pages": [3]
			}]
		}`,
	}}
	result, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider: provider,
		Renderer: fakeRenderer{images: []PageImage{
			{Page: 1, Path: filepath.Join(dir, "page-1.png"), MIMEType: "image/png"},
			{Page: 2, Path: filepath.Join(dir, "page-2.png"), MIMEType: "image/png"},
			{Page: 3, Path: filepath.Join(dir, "page-3.png"), MIMEType: "image/png"},
		}},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: adapters.NewRegistry(seedAdapter),
		OutputDir:       dir,
		MaxConcurrency:  2,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v, report=%#v", err, result.ValidationReport)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("expected one VLM request per page, got %d", len(provider.requests))
	}
	if len(provider.requests[0].Images) != 1 || provider.requests[0].Images[0].Page != 1 {
		t.Fatalf("first request should only contain first page: %#v", provider.requests[0].Images)
	}
	for _, req := range provider.requests[1:] {
		if !strings.Contains(req.Prompt, "前置 seed 页面已经确认了表格结构") || !strings.Contains(req.Prompt, "记账日期") {
			t.Fatalf("continuation prompt missing confirmed structure: %s", req.Prompt)
		}
	}
	if got := len(result.Tables[0].Rows); got != 3 {
		t.Fatalf("expected 3 data rows with duplicate header removed, got %d", got)
	}
	for i, expected := range []string{"2024-01-01", "2024-01-02", "2024-01-03"} {
		if result.Tables[0].Rows[i][0] == nil || *result.Tables[0].Rows[i][0] != expected {
			t.Fatalf("row %d out of order: %#v", i, result.Tables[0].Rows)
		}
	}
	if strings.Contains(string(result.Artifacts.CSVBytes), "\n\n") {
		t.Fatalf("csv should not contain blank lines: %q", string(result.Artifacts.CSVBytes))
	}
	auditDir := filepath.Join(dir, result.TaskID, "intermediate", "audit")
	for _, name := range []string{
		"seed_pages_request.json",
		"seed_pages_response.json",
		"page_2_request.json",
		"page_2_response.json",
		"page_3_request.json",
		"page_3_response.json",
	} {
		if _, statErr := os.Stat(filepath.Join(auditDir, name)); statErr != nil {
			t.Fatalf("expected per-page audit file %s: %v", name, statErr)
		}
	}
}

func TestConvertCanDisableSeedParsingPerAdapter(t *testing.T) {
	dir := t.TempDir()
	provider := &pageAwareProvider{responses: map[int]string{
		1: `{
			"metadata": {
				"title": "招商银行交易流水",
				"english_title": "Transaction Statement of China Merchants Bank",
				"statement_period": "2024/01/01-2024/01/31",
				"name": "张三",
				"account_no": "6222 **** 1234",
				"account_type": "ALL/全币种",
				"sub_branch": "北京支行",
				"application_time": "2024-02-01 12:00",
				"verification_code": "9T0000"
			},
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [["2024-01-01", "CNY", "1.00", "10.00", "第一页", "对手1"]],
				"source_pages": [1]
			}]
		}`,
		2: `{
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [["2024-01-02", "CNY", "2.00", "12.00", "第二页", "对手2"]],
				"source_pages": [1]
			}]
		}`,
	}}
	registry := adapters.NewRegistry(adapters.Adapter{
		Key:              "no_seed",
		Name:             "No Seed",
		Prompt:           "parse all pages",
		SeedPages:        0,
		RequiredMetadata: []string{"title", "english_title", "statement_period", "name", "account_no", "account_type", "sub_branch", "application_time", "verification_code"},
		ExpectedTables: []adapters.TableSpec{{
			AllowedHeaders: [][]string{{"记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"}},
			MinColumns:     6,
		}},
	})
	result, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider: provider,
		Renderer: fakeRenderer{images: []PageImage{
			{Page: 1, Path: filepath.Join(dir, "page-1.png"), MIMEType: "image/png"},
			{Page: 2, Path: filepath.Join(dir, "page-2.png"), MIMEType: "image/png"},
		}},
		AdapterKey:      "no_seed",
		AdapterRegistry: registry,
		OutputDir:       dir,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v, report=%#v", err, result.ValidationReport)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected seed disabled adapter to parse pages independently, got %d request(s)", len(provider.requests))
	}
	if len(result.Tables[0].Rows) != 2 {
		t.Fatalf("expected merged independent page rows, got %#v", result.Tables[0].Rows)
	}
	if len(result.Tables[0].SourcePages) != 2 || result.Tables[0].SourcePages[0] != 1 || result.Tables[0].SourcePages[1] != 2 {
		t.Fatalf("expected source_pages to be overridden from actual page numbers, got %#v", result.Tables[0].SourcePages)
	}
	auditDir := filepath.Join(dir, result.TaskID, "intermediate", "audit")
	for _, name := range []string{
		"page_1_request.json",
		"page_1_response.json",
		"page_2_request.json",
		"page_2_response.json",
	} {
		if _, statErr := os.Stat(filepath.Join(auditDir, name)); statErr != nil {
			t.Fatalf("expected per-page audit file %s: %v", name, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(auditDir, "all_pages_request.json")); !os.IsNotExist(statErr) {
		t.Fatalf("did not expect all_pages_request.json, stat err=%v", statErr)
	}
}

func TestConvertAcceptsMultiplePDFInputs(t *testing.T) {
	dir := t.TempDir()
	renderer := &recordingRenderer{}
	provider := &pageAwareProvider{responses: map[int]string{
		1: `{
			"metadata": {
				"title": "招商银行交易流水",
				"english_title": "Transaction Statement of China Merchants Bank",
				"statement_period": "2024/01/01-2024/01/31",
				"name": "张三",
				"account_no": "6222 **** 1234",
				"account_type": "ALL/全币种",
				"sub_branch": "北京支行",
				"application_time": "2024-02-01 12:00",
				"verification_code": "9T0000"
			},
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [["2024-01-01", "CNY", "1.00", "10.00", "多文件1", "对手1"]],
				"source_pages": [1]
			}]
		}`,
		2: `{
			"tables": [{
				"name": "交易明细",
				"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
				"rows": [["2024-01-02", "CNY", "2.00", "12.00", "多文件2", "对手2"]],
				"source_pages": [2]
			}]
		}`,
	}}
	result, err := Convert(context.Background(), Input{Files: []InputFile{
		{Path: "page-a.pdf", FileName: "page-a.pdf"},
		{Path: "page-b.pdf", FileName: "page-b.pdf"},
	}}, Options{
		Provider:        provider,
		Renderer:        renderer,
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       dir,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v, report=%#v", err, result.ValidationReport)
	}
	if len(renderer.inputs) != 2 {
		t.Fatalf("expected renderer to be called once per input PDF, got %d", len(renderer.inputs))
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected provider to receive one request per rendered page, got %d", len(provider.requests))
	}
	pages := map[int]bool{}
	for _, req := range provider.requests {
		if len(req.Images) != 1 {
			t.Fatalf("expected one rendered page per request, got %#v", req.Images)
		}
		pages[req.Images[0].Page] = true
	}
	if !pages[1] || !pages[2] {
		t.Fatalf("expected continuous page numbering across requests, got %#v", provider.requests)
	}
	if len(result.Source.Files) != 2 || result.Source.Files[0].FileName != "page-a.pdf" || result.Source.Files[1].FileName != "page-b.pdf" {
		t.Fatalf("expected multiple source files in result, got %#v", result.Source)
	}
}

func TestConvertWritesAuditArtifactsOnParseError(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeProvider{text: `{"metadata":`}
	_, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:        provider,
		Renderer:        fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       dir,
	})
	if err == nil || !strings.Contains(err.Error(), "bill_file_converter.log") {
		t.Fatalf("expected parse error with log path guidance, got %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read output dir: %v", readErr)
	}
	if len(entries) != 1 || !entries[0].IsDir() || !taskIDPattern.MatchString(entries[0].Name()) {
		t.Fatalf("expected exactly one task dir, got %#v", entries)
	}
	taskDir := filepath.Join(dir, entries[0].Name())
	auditDir := filepath.Join(taskDir, "intermediate", "audit")
	for _, name := range []string{"page_1_request.json", "page_1_response.json", "failure.json"} {
		if _, statErr := os.Stat(filepath.Join(auditDir, name)); statErr != nil {
			t.Fatalf("expected audit file %s after parse error: %v", name, statErr)
		}
	}
	logData, statErr := os.ReadFile(filepath.Join(taskDir, "intermediate", "bill_file_converter.log"))
	if statErr != nil {
		t.Fatalf("expected bill_file_converter.log after parse error: %v", statErr)
	}
	logText := string(logData)
	if !strings.Contains(logText, "[ERROR]") {
		t.Fatalf("expected ERROR log level after parse error, got %q", logText)
	}
	if strings.Contains(logText, "BEGIN") || strings.Contains(logText, "END") {
		t.Fatalf("log should no longer contain inline JSON blocks, got %q", logText)
	}
	if !strings.Contains(logText, "wrote failure summary to") {
		t.Fatalf("log should reference failure.json, got %q", logText)
	}
}

func TestTableAcceptsNonNumericSourcePages(t *testing.T) {
	var doc Document
	err := json.Unmarshal([]byte(`{
		"tables": [{
			"headers": ["A"],
			"rows": [["B"]],
			"source_pages": ["当前图片对应的页码"]
		}]
	}`), &doc)
	if err != nil {
		t.Fatalf("source_pages placeholder should not fail unmarshal: %v", err)
	}
	if len(doc.Tables[0].SourcePages) != 0 {
		t.Fatalf("expected non-numeric source_pages to be ignored, got %#v", doc.Tables[0].SourcePages)
	}
}

func TestConvertAllowsCMBDebitWithoutCustomerSummary(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeProvider{text: `{
		"metadata": {
			"title": "招商银行交易流水",
			"english_title": "Transaction Statement of China Merchants Bank",
			"statement_period": "2025-01-01 -- 2025-06-30",
			"name": "张三",
			"account_no": "6214865096001024",
			"account_type": "ALL/全币种",
			"sub_branch": "北京支行",
			"application_time": "2025-10-20 20:53",
			"verification_code": "9T0000"
		},
		"title": "招商银行交易流水",
		"tables": [{
			"name": "交易明细",
			"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息"],
			"rows": [["2025-01-01", "CNY", "8,468.00", "53,456.81", "代发住房公积金", "北京住房公积金管理中心 110905276110801"]],
			"source_pages": [1]
		}]
	}`}
	result, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:        provider,
		Renderer:        fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       dir,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v, report=%#v", err, result.ValidationReport)
	}
	if len(result.Tables[0].Headers) != 6 {
		t.Fatalf("expected 6-column table, got %#v", result.Tables[0].Headers)
	}
}

func TestConvertRejectsNonPDF(t *testing.T) {
	_, err := Convert(context.Background(), Input{Path: "bill.eml"}, Options{
		Provider:        &fakeProvider{},
		Renderer:        fakeRenderer{},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "only supports PDF") {
		t.Fatalf("expected PDF-only error, got %v", err)
	}
}

func TestConvertRejectsUnknownAdapter(t *testing.T) {
	_, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:        &fakeProvider{},
		Renderer:        fakeRenderer{},
		AdapterKey:      "unknown",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported bill type") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestConvertReturnsValidationErrorAndStillWritesJSON(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	provider := &fakeProvider{text: `{"metadata": {}, "tables": []}`}
	result, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:        provider,
		Renderer:        fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       dir,
		LogWriter:       &logs,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !result.ValidationReport.HasErrors() {
		t.Fatalf("expected validation errors: %#v", result.ValidationReport)
	}
	if _, statErr := os.Stat(filepath.Join(dir, result.TaskID, "result", "result.json")); statErr != nil {
		t.Fatalf("expected result.json to be written: %v", statErr)
	}
	if !strings.Contains(logs.String(), "validation error:") {
		t.Fatalf("expected validation errors in logs, got %q", logs.String())
	}
	if !strings.Contains(logs.String(), "\033[31m") {
		t.Fatalf("expected error logs to be red in std output, got %q", logs.String())
	}
	logData, readErr := os.ReadFile(filepath.Join(dir, result.TaskID, "intermediate", "bill_file_converter.log"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(logData), "\033[31m") {
		t.Fatalf("file log should not contain ANSI colors, got %q", string(logData))
	}
}

func TestColorizeStdLogLine(t *testing.T) {
	if got := colorizeStdLogLine(LogWarning, "warn"); got != "\033[33mwarn\033[0m" {
		t.Fatalf("warning color mismatch: %q", got)
	}
	if got := colorizeStdLogLine(LogError, "err"); got != "\033[31merr\033[0m" {
		t.Fatalf("error color mismatch: %q", got)
	}
	if got := colorizeStdLogLine(LogInfo, "info"); got != "info" {
		t.Fatalf("info should not be colored: %q", got)
	}
}

func TestConvertSkipCSV(t *testing.T) {
	dir := t.TempDir()
	provider := &fakeProvider{text: `{
		"metadata": {
			"title": "招商银行交易流水",
			"english_title": "Transaction Statement of China Merchants Bank",
			"statement_period": "2024",
			"name": "张三",
			"account_no": "6222",
			"account_type": "ALL/全币种",
			"sub_branch": "北京支行",
			"application_time": "2024-01-01",
			"verification_code": "9T0000"
		},
		"tables": [{
			"headers": ["记账日期", "货币", "交易金额", "联机余额", "交易摘要", "对手信息", "客户摘要"],
			"rows": [["1", "CNY", "2", "3", "4", "5", "6"]],
			"source_pages": [1]
		}]
	}`}
	result, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:        provider,
		Renderer:        fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey:      "cmb_debit",
		AdapterRegistry: builtinRegistry(),
		OutputDir:       dir,
		SkipCSV:         true,
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.Artifacts.CSVPath != "" || len(result.Artifacts.CSVBytes) != 0 {
		t.Fatalf("expected csv to be skipped: %#v", result.Artifacts)
	}
	if _, err := os.Stat(filepath.Join(dir, result.TaskID, "result", "result.csv")); !os.IsNotExist(err) {
		t.Fatalf("expected no result.csv, stat err=%v", err)
	}
}
