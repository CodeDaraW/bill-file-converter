package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deb-sig/bill-file-converter/core/adapters"
)

type fakeMinerU struct {
	result MinerUParseResult
	err    error
	input  Input
}

func (m *fakeMinerU) Parse(_ context.Context, input Input) (MinerUParseResult, error) {
	m.input = input
	return m.result, m.err
}

func TestConvertUsesMinerUContentListAndWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "bill.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.7"), 0o644); err != nil {
		t.Fatal(err)
	}
	page := 0
	minerU := &fakeMinerU{result: MinerUParseResult{
		RawRequest:  `{"request":true}`,
		RawResponse: `{"response":true}`,
		ContentList: []MinerUContent{
			{Type: "text", Text: " 招商银行交易流水 \n 招商银行交易流水 "},
			{Type: "text", Text: "招商银行交易流水"},
			{Type: "table", PageIndex: &page, TableBody: `<table>
				<tr><th>记账日期</th><th>货币</th><th>交易金额</th><th>联机余额</th><th>交易摘要</th><th>对手信息</th></tr>
				<tr><td>Date</td><td>Currency</td><td>Transaction Amount</td><td>Balance</td><td>Transaction Type</td><td>Counter Party</td></tr>
				<tr><td>2026-01-01</td><td>CNY</td><td>1.00</td><td>9.00</td><td>消费</td><td></td></tr>
				<tr><td>记账日期</td><td>货币</td><td>交易金额</td><td>联机余额</td><td>交易摘要</td><td>对手信息</td></tr>
				<tr><td>Date</td><td>Currency</td><td>Transaction Amount</td><td>Balance</td><td>Transaction Type</td><td>Counter Party</td></tr>
				<tr><td>Date</td><td>Currency</td><td>Transaction Amount</td><td>Balance</td><td>Transaction Ty</td><td></td></tr>
				<tr><td>Date</td><td>Currency</td><td>Transaction Amount</td><td>Balance</td><td>Transaction Type</td><td>招商银行股份有限公司</td></tr>
				<tr><td>2026-01-02</td><td>CNY</td><td>-2.00</td><td>7.00</td><td>退款</td><td>张三</td></tr>
			</table>`},
		},
	}}
	result, err := Convert(context.Background(), Input{Path: pdf, FileName: "bill.pdf"}, Options{
		MinerU:          minerU,
		AdapterKey:      "cmb_debit",
		AdapterRegistry: testRegistry(),
		OutputDir:       filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatalf("%v: %#v", err, result.ValidationReport)
	}
	if result.Metadata["raw_text"] != "招商银行交易流水 招商银行交易流水\n招商银行交易流水" {
		t.Fatalf("unexpected raw_text: %#v", result.Metadata)
	}
	if len(result.Tables) != 1 || len(result.Tables[0].Rows) != 2 {
		t.Fatalf("unexpected tables: %#v", result.Tables)
	}
	if result.Tables[0].SourcePages[0] != 1 {
		t.Fatalf("expected source page 1, got %#v", result.Tables[0].SourcePages)
	}
	for _, path := range []string{
		result.Artifacts.JSONPath,
		result.Artifacts.CSVPath,
		result.Artifacts.ContentListPath,
		filepath.Join(filepath.Dir(result.Artifacts.AuditDir), "bill_file_converter.log"),
		filepath.Join(result.Artifacts.AuditDir, "mineru_request.json"),
		filepath.Join(result.Artifacts.AuditDir, "mineru_response.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
	csvData, err := os.ReadFile(result.Artifacts.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(csvData), "记账日期,货币,交易金额,联机余额,交易摘要,对手信息") != 1 {
		t.Fatalf("expected repeated header to be removed: %s", csvData)
	}
	if strings.Contains(string(csvData), "Date,Currency,Transaction Amount,Balance,Transaction Type,Counter Party") {
		t.Fatalf("expected English header alias to be removed: %s", csvData)
	}
	if strings.Contains(string(csvData), "Transaction Ty") {
		t.Fatalf("expected truncated English header alias to be removed: %s", csvData)
	}
	if strings.Contains(string(csvData), "招商银行股份有限公司") {
		t.Fatalf("expected first-column Date header row to be removed: %s", csvData)
	}
}

func TestConvertMultiplePDFs(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "1.pdf")
	second := filepath.Join(dir, "2.pdf")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("%PDF-1.7"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	minerU := &fakeMinerU{result: MinerUParseResult{
		ContentList: []MinerUContent{
			{Type: "table", TableBody: `<table>
				<tr><th>交易日期</th><th>交易时间</th><th>交易摘要</th><th>交易金额</th><th>本次余额</th><th>对手信息</th><th>日 志 号</th><th>交易渠道</th><th>交易附言</th></tr>
				<tr><td>20260101</td><td>12:00:00</td><td>转账</td><td>1.00</td><td>2.00</td><td>--</td><td>1234567890</td><td>网银</td><td></td></tr>
			</table>`},
		},
	}}
	result, err := Convert(context.Background(), Input{Files: []InputFile{
		{Path: first, FileName: "1.pdf"},
		{Path: second, FileName: "2.pdf"},
	}}, Options{
		MinerU:          minerU,
		AdapterKey:      "abc_debit",
		AdapterRegistry: adapters.BuiltinRegistry(),
		OutputDir:       filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(minerU.input.Files) != 2 {
		t.Fatalf("expected two input files, got %#v", minerU.input)
	}
	if got := result.Tables[0].Headers[6]; got != "日志号" {
		t.Fatalf("expected header spaces to be stripped via profile headers, got %q", got)
	}
}

func TestCmbCreditSkipsSectionRowsAndSummaryRows(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "bill.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.7"), 0o644); err != nil {
		t.Fatal(err)
	}
	minerU := &fakeMinerU{result: MinerUParseResult{
		ContentList: []MinerUContent{
			{Type: "text", Text: "招商银行信用卡对账单（个人消费卡账户 2025年02月）（补）"},
			{Type: "table", TableBody: `<table>
				<tr><th>交易日 SOLD</th><th>记账日 POSTED</th><th>交易摘要 DESCRIPTION</th><th>人民币金额 RMB AMOUNT</th><th>卡号末四位 CARD NO(Last 4digits)</th><th>交易地金额 Original Tran Amount</th></tr>
				<tr><td>还款</td><td></td><td></td><td></td><td></td><td></td></tr>
				<tr><td>还款</td><td>还款</td><td>还款</td><td>还款</td><td>还款</td><td>还款</td></tr>
				<tr><td></td><td>01/27</td><td>银联转账还款</td><td>-29.50</td><td>2508</td><td>-29.50</td></tr>
				<tr><td>消费</td><td></td><td></td><td></td><td></td><td></td></tr>
				<tr><td>01/12</td><td>01/13</td><td>支付宝-上海拉扎斯信息科技有限公司</td><td>1.70</td><td>2508</td><td>1.70(CN)</td></tr>
				<tr><td>本期还款总额Current Balance</td><td>=</td><td>上期账单金额Balance B/F</td><td>-</td><td>上期还款金额Payment</td><td>+</td></tr>
			</table>`},
		},
	}}
	result, err := Convert(context.Background(), Input{Path: pdf, FileName: "bill.pdf"}, Options{
		MinerU:          minerU,
		AdapterKey:      "cmb_credit",
		AdapterRegistry: adapters.BuiltinRegistry(),
		OutputDir:       filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatal(err)
	}
	csvData, err := os.ReadFile(result.Artifacts.CSVPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(csvData)
	if !strings.HasPrefix(got, "交易日,记账日,交易摘要,人民币金额,卡号末四位,交易地金额\n") {
		t.Fatalf("unexpected csv prefix: %s", got)
	}
	if strings.Contains(got, "\n还款,") || strings.Contains(got, "\n消费,") {
		t.Fatalf("expected section rows to be skipped: %s", got)
	}
	if strings.Contains(got, "本期还款总额") {
		t.Fatalf("expected summary rows to be skipped: %s", got)
	}
}

func TestConvertValidationFailsWithoutMatchingTables(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "bill.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.7"), 0o644); err != nil {
		t.Fatal(err)
	}
	minerU := &fakeMinerU{result: MinerUParseResult{
		ContentList: []MinerUContent{{Type: "table", TableBody: `<table><tr><th>A</th></tr><tr><td>B</td></tr></table>`}},
	}}
	result, err := Convert(context.Background(), Input{Path: pdf, FileName: "bill.pdf"}, Options{
		MinerU:          minerU,
		AdapterKey:      "cmb_debit",
		AdapterRegistry: testRegistry(),
		OutputDir:       filepath.Join(dir, "output"),
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if result.Artifacts.JSONPath == "" {
		t.Fatal("expected result JSON to be written before validation failure")
	}
}

func TestValueMatchesGuardFormatStrictDates(t *testing.T) {
	cases := []struct {
		value  string
		format string
		want   bool
	}{
		{value: "2024-02-29", format: "YYYY-MM-DD", want: true},
		{value: "2026-01-01", format: "YYYY-MM-DD", want: true},
		{value: "2025-02-29", format: "YYYY-MM-DD", want: false},
		{value: "2025-2-09", format: "YYYY-MM-DD", want: false},
		{value: "20240229", format: "YYYYMMDD", want: true},
		{value: "20250229", format: "YYYYMMDD", want: false},
		{value: "2025029", format: "YYYYMMDD", want: false},
		{value: "02/29", format: "MM/DD", want: true},
		{value: "02/31", format: "MM/DD", want: false},
		{value: "13/01", format: "MM/DD", want: false},
		{value: "2/09", format: "MM/DD", want: false},
		{value: "02/09", format: "unknown", want: false},
	}
	for _, tc := range cases {
		if got := valueMatchesGuardFormat(tc.value, tc.format); got != tc.want {
			t.Fatalf("valueMatchesGuardFormat(%q, %q) = %v, want %v", tc.value, tc.format, got, tc.want)
		}
	}
}

func TestParseHTMLTableSpans(t *testing.T) {
	rows, err := parseHTMLTable(`<table>
		<tr><th rowspan="2">A</th><th colspan="2">B</th></tr>
		<tr><th>C</th><th>D</th></tr>
		<tr><td>1</td><td></td><td>3</td></tr>
	</table>`)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"A", "B", "B"}, {"A", "C", "D"}, {"1", "", "3"}}
	if len(rows) != len(want) {
		t.Fatalf("rows=%#v", rows)
	}
	for i := range want {
		if strings.Join(rows[i], "|") != strings.Join(want[i], "|") {
			t.Fatalf("row %d = %#v, want %#v", i, rows[i], want[i])
		}
	}
}

func TestColorizeStdLogLine(t *testing.T) {
	if got := colorizeStdLogLine(LogWarning, "warn"); got != "\033[33mwarn\033[0m" {
		t.Fatalf("warning color = %q", got)
	}
	if got := colorizeStdLogLine(LogError, "err"); got != "\033[31merr\033[0m" {
		t.Fatalf("error color = %q", got)
	}
	if got := colorizeStdLogLine(LogInfo, "info"); got != "info" {
		t.Fatalf("info color = %q", got)
	}
}

func testRegistry() *adapters.Registry {
	cmb, err := adapters.BuiltinRegistry().MustGet("cmb_debit")
	if err != nil {
		panic(err)
	}
	cmb.RemoveImages = false
	abc, err := adapters.BuiltinRegistry().MustGet("abc_debit")
	if err != nil {
		panic(err)
	}
	return adapters.NewRegistry(cmb, abc)
}
