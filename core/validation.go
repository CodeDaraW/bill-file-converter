package core

import (
	"fmt"

	"github.com/deb-sig/bill-file-converter/core/adapters"
)

func ValidateDocument(doc Document, adapter adapters.Adapter) ValidationReport {
	var report ValidationReport
	if len(doc.Tables) == 0 {
		report.Errors = append(report.Errors, "no tables were extracted")
	}

	for tableIdx, table := range doc.Tables {
		if len(table.Headers) == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("table %d has no headers", tableIdx+1))
		}
		minColumns := minColumnsForTable(adapter, table)
		if minColumns > 0 && len(table.Headers) < minColumns {
			report.Errors = append(report.Errors, fmt.Sprintf("table %d has %d headers, expected at least %d", tableIdx+1, len(table.Headers), minColumns))
		}
		if allowedHeaders := allowedHeadersForTable(adapter, table); len(allowedHeaders) > 0 && !matchesAnyHeaders(table.Headers, allowedHeaders) {
			report.Errors = append(report.Errors, fmt.Sprintf("table %d headers do not match adapter profile: got %q, expected one of %q", tableIdx+1, table.Headers, allowedHeaders))
		}
		for rowIdx, row := range table.Rows {
			if len(row) != len(table.Headers) {
				report.Errors = append(report.Errors, fmt.Sprintf("table %d row %d has %d cells, expected %d", tableIdx+1, rowIdx+1, len(row), len(table.Headers)))
			}
		}
		if len(table.SourcePages) == 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("table %d has no source_pages", tableIdx+1))
		}
		report.Warnings = append(report.Warnings, table.Warnings...)
	}

	return report
}

func minColumnsForTable(adapter adapters.Adapter, table Table) int {
	for _, spec := range adapter.ExpectedTables {
		if spec.Name != "" && spec.Name != table.Name {
			continue
		}
		if len(spec.Headers) > 0 {
			return len(spec.Headers)
		}
		return spec.MinColumns
	}
	return 0
}

func allowedHeadersForTable(adapter adapters.Adapter, table Table) [][]string {
	for _, spec := range adapter.ExpectedTables {
		if spec.Name != "" && spec.Name != table.Name {
			continue
		}
		if len(spec.AllowedHeaders) > 0 {
			return spec.AllowedHeaders
		}
		if len(spec.Headers) > 0 {
			return [][]string{spec.Headers}
		}
		return nil
	}
	return nil
}

func matchesAnyHeaders(headers []string, allowed [][]string) bool {
	for _, candidate := range allowed {
		if equalStrings(headers, candidate) {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
