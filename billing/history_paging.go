package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const (
	historyMaxBody  = 8 << 20
	historyMaxBytes = 32 << 20
	historyMaxPages = 100
)

func (c *Client) fetchHistory(ctx context.Context, op, resource string, dates historyDates, page, size int) (json.RawMessage, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	query := url.Values{"dateStart": {dates.start}, "dateEnd": {dates.end}, "pageNumber": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)}}
	var raw json.RawMessage
	var credentials []string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: op, Method: http.MethodGet, URL: c.route([]string{"gateway", "api", "v1", resource}, query), OK: []int{http.StatusOK}, MaxBody: historyMaxBody, NoRedirect: true, WithholdMessage: historyWithheld, SentCredentials: &credentials, KeepObservedStatus: true,
		ClassifyError: func(status int, _ string) string { return historyStatusCode(status) },
	}, &raw)
	if historyHasNumericCredential(credentials) {
		return nil, nil, &core.APIError{Operation: op, Code: "InvalidResponse", Message: historyWithheld}
	}
	if err != nil {
		return nil, nil, historyRequestError(op, status, err)
	}
	return raw, credentials, nil
}

func validateHistoryMetadata(page *historyPage, number, size int, all bool, op string) error {
	m := page.Metadata
	if *m.Number <= 0 || *m.Size <= 0 || *m.Size > size || *m.Total < 0 || *m.Pages < 0 {
		return historyInvalid(op, errHistoryShape)
	}
	expectedPages := 0
	if *m.Total > 0 {
		expectedPages = (*m.Total-1) / *m.Size + 1
	}
	if *m.Pages != expectedPages || len(page.Content) > *m.Size {
		return historyInvalid(op, errHistoryShape)
	}
	if *m.Number != number {
		if all {
			return historyIncomplete(op)
		}
		return historyInvalid(op, errHistoryShape)
	}
	expectedRows := 0
	if number <= expectedPages {
		expectedRows = *m.Size
		if number == expectedPages {
			expectedRows = *m.Total - (expectedPages-1)**m.Size
		}
	}
	if len(page.Content) != expectedRows {
		if all {
			return historyIncomplete(op)
		}
		return historyInvalid(op, errHistoryShape)
	}
	return nil
}

func listHistory[T any](ctx context.Context, c *Client, op, resource string, in ListTransactionsInput, decode func(json.RawMessage) (T, error), id func(T) int64, sanitize func(*T, historyRedactor)) (*core.PagedList[T], error) {
	dates, err := resolveHistoryDates(op, in.StartDate, in.EndDate)
	if err != nil {
		return nil, err
	}
	if in.All && in.Page > 1 {
		return nil, fmt.Errorf("%w: %s All requires page 1", core.ErrInvalidInput, op)
	}
	number, size := in.Page, in.Size
	if number <= 0 {
		number = 1
	}
	if size <= 0 || size > 50 {
		size = 50
	}
	items := make([]T, 0)
	seen := make(map[int64]struct{})
	var credentials []string
	var frozen historyMetadata
	bytes := 0
	var output core.PagedList[T]
	for {
		raw, sent, err := c.fetchHistory(ctx, op, resource, dates, number, size)
		if err != nil {
			return nil, err
		}
		credentials = retainHistoryCredentials(credentials, sent)
		data, code, err := decodeHistoryEnvelope(raw)
		if err != nil {
			return nil, historyInvalid(op, err)
		}
		if code != "" {
			return nil, &core.APIError{Operation: op, StatusCode: 200, Code: code, Message: historyWithheld}
		}
		if in.All && len(raw) > historyMaxBytes-bytes {
			return nil, historyIncomplete(op)
		}
		bytes += len(raw)
		page, err := decodeHistoryPage(data)
		if err != nil {
			return nil, historyInvalid(op, err)
		}
		if err := validateHistoryMetadata(page, number, size, in.All, op); err != nil {
			return nil, err
		}
		if in.All && number > 1 && (*page.Metadata.Pages != *frozen.Pages || *page.Metadata.Total != *frozen.Total || *page.Metadata.Size != *frozen.Size) {
			return nil, historyIncomplete(op)
		}
		m := page.Metadata
		if in.All && number == 1 {
			if *m.Pages > historyMaxPages || *m.Total > historyMaxPages**m.Size {
				return nil, historyIncomplete(op)
			}
			frozen = *m
		}
		local := make(map[int64]struct{})
		for _, row := range page.Content {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			item, err := decode(row)
			if err != nil {
				return nil, historyInvalid(op, err)
			}
			key := id(item)
			if _, exists := local[key]; exists {
				return nil, historyInvalid(op, errHistoryShape)
			}
			local[key] = struct{}{}
			items = append(items, item)
		}
		for key := range local {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if _, exists := seen[key]; exists {
				return nil, historyIncomplete(op)
			}
			seen[key] = struct{}{}
		}
		output = core.PagedList[T]{Page: number, PageSize: *m.Size, TotalPage: *m.Pages, TotalItem: *m.Total}
		if !in.All || number >= *frozen.Pages {
			break
		}
		number++
	}
	if in.All {
		if len(items) != *frozen.Total {
			return nil, historyIncomplete(op)
		}
		output = core.PagedList[T]{Page: 1, PageSize: len(items), TotalPage: 1, TotalItem: len(items)}
	}
	// Later sends can expose credentials already present in earlier rows.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	redactor := newHistoryRedactor(credentials)
	for i := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sanitize(&items[i], redactor)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output.Items = items
	return &output, nil
}
