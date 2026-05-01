package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRenderer struct {
	images []PageImage
	err    error
}

func (r fakeRenderer) Check(context.Context) error { return r.err }

func (r fakeRenderer) Render(context.Context, Input, string) ([]PageImage, error) {
	return r.images, r.err
}

type fakeProvider struct {
	text string
	req  VLMRequest
}

func (p *fakeProvider) Generate(_ context.Context, req VLMRequest) (VLMResponse, error) {
	p.req = req
	return VLMResponse{Text: p.text, Raw: `{"raw":true}`}, nil
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
		Provider:   provider,
		Renderer:   fakeRenderer{images: []PageImage{{Page: 1, Path: imagePath, MIMEType: "image/png"}}},
		AdapterKey: "cmb_debit",
		OutputDir:  dir,
		LogWriter:  &logs,
		TaskID:     "20250101-120000-abcdef12",
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.Metadata["account_no"] != "6222 **** 1234" {
		t.Fatalf("metadata not preserved: %#v", result.Metadata)
	}
	if result.TaskID != "20250101-120000-abcdef12" {
		t.Fatalf("task id not preserved: %q", result.TaskID)
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
	taskDir := filepath.Join(dir, "20250101-120000-abcdef12")
	csvData, err := os.ReadFile(filepath.Join(taskDir, "result.csv"))
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
	jsonData, err := os.ReadFile(filepath.Join(taskDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jsonData), `"metadata"`) {
		t.Fatalf("json missing metadata: %s", jsonData)
	}
	if !strings.Contains(logs.String(), "[20250101-120000-abcdef12]") || !strings.Contains(logs.String(), "T") || !strings.Contains(logs.String(), "rendering PDF pages") || !strings.Contains(logs.String(), "done") {
		t.Fatalf("expected progress logs, got %q", logs.String())
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
		Provider:   provider,
		Renderer:   fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey: "cmb_debit",
		OutputDir:  dir,
		TaskID:     "20250101-120000-abcdef13",
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
		Provider:   &fakeProvider{},
		Renderer:   fakeRenderer{},
		AdapterKey: "cmb_debit",
		OutputDir:  t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "only supports PDF") {
		t.Fatalf("expected PDF-only error, got %v", err)
	}
}

func TestConvertRejectsUnknownAdapter(t *testing.T) {
	_, err := Convert(context.Background(), Input{Path: "bill.pdf"}, Options{
		Provider:   &fakeProvider{},
		Renderer:   fakeRenderer{},
		AdapterKey: "unknown",
		OutputDir:  t.TempDir(),
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
		Provider:   provider,
		Renderer:   fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey: "cmb_debit",
		OutputDir:  dir,
		LogWriter:  &logs,
		TaskID:     "20250101-120000-abcdef14",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !result.ValidationReport.HasErrors() {
		t.Fatalf("expected validation errors: %#v", result.ValidationReport)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "20250101-120000-abcdef14", "result.json")); statErr != nil {
		t.Fatalf("expected result.json to be written: %v", statErr)
	}
	if !strings.Contains(logs.String(), "validation error:") {
		t.Fatalf("expected validation errors in logs, got %q", logs.String())
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
		Provider:   provider,
		Renderer:   fakeRenderer{images: []PageImage{{Page: 1, Path: filepath.Join(dir, "page.png"), MIMEType: "image/png"}}},
		AdapterKey: "cmb_debit",
		OutputDir:  dir,
		SkipCSV:    true,
		TaskID:     "20250101-120000-abcdef15",
	})
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	if result.Artifacts.CSVPath != "" || len(result.Artifacts.CSVBytes) != 0 {
		t.Fatalf("expected csv to be skipped: %#v", result.Artifacts)
	}
	if _, err := os.Stat(filepath.Join(dir, "20250101-120000-abcdef15", "result.csv")); !os.IsNotExist(err) {
		t.Fatalf("expected no result.csv, stat err=%v", err)
	}
}
