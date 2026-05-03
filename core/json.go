package core

import (
	"encoding/json"
	"strconv"
	"strings"
)

func (t *Table) UnmarshalJSON(data []byte) error {
	type tableAlias struct {
		Name        string          `json:"name,omitempty"`
		Headers     []string        `json:"headers"`
		Rows        [][]*string     `json:"rows"`
		SourcePages json.RawMessage `json:"source_pages,omitempty"`
		Warnings    []string        `json:"warnings,omitempty"`
	}
	var decoded tableAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	t.Name = decoded.Name
	t.Headers = decoded.Headers
	t.Rows = decoded.Rows
	t.Warnings = decoded.Warnings
	t.SourcePages = parseSourcePages(decoded.SourcePages)
	return nil
}

func parseSourcePages(raw json.RawMessage) []int {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var ints []int
	if err := json.Unmarshal(raw, &ints); err == nil {
		return ints
	}
	var values []any
	if err := json.Unmarshal(raw, &values); err == nil {
		pages := make([]int, 0, len(values))
		for _, value := range values {
			switch typed := value.(type) {
			case float64:
				pages = append(pages, int(typed))
			case string:
				if page, ok := parseSourcePageString(typed); ok {
					pages = append(pages, page)
				}
			}
		}
		return pages
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		if page, ok := parseSourcePageString(value); ok {
			return []int{page}
		}
	}
	return nil
}

func parseSourcePageString(value string) (int, bool) {
	page, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return page, true
}
