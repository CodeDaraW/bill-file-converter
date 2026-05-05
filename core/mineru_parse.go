package core

import (
	"context"
	"encoding/json"
	"strings"
)

func parseMinerUInInputOrder(ctx context.Context, client MinerUClient, files []InputFile) (MinerUParseResult, error) {
	var combined MinerUParseResult
	var rawRequests []json.RawMessage
	var rawResponses []json.RawMessage
	pageOffset := 0
	for _, file := range files {
		result, err := client.Parse(ctx, file)
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
