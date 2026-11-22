package monitor

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

const maxMonitorNameSearchPages = 1000

func nameSearchError(op string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "name search was incomplete or ambiguous"}
}

func findMonitorName[T any](ctx context.Context, op, target string, firstPage int, identity func(T) (string, string), fetch func(int) (*core.PagedList[T], error)) (*T, error) {
	seen := make(map[string]bool)
	var found *T
	total, pages, size := -1, -1, -1
	for page := firstPage; page-firstPage < maxMonitorNameSearchPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, err := fetch(page)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if out == nil || out.Items == nil || out.Page != page || out.PageSize <= 0 || out.TotalItem < 0 || out.TotalPage < 0 || out.TotalPage > maxMonitorNameSearchPages || len(out.Items) > out.PageSize {
			return nil, nameSearchError(op)
		}
		if page == firstPage {
			total, pages, size = out.TotalItem, out.TotalPage, out.PageSize
		} else if total != out.TotalItem || pages != out.TotalPage || size != out.PageSize {
			return nil, nameSearchError(op)
		}
		for i := range out.Items {
			id, name := identity(out.Items[i])
			if id == "" || name == "" || seen[id] {
				return nil, nameSearchError(op)
			}
			seen[id] = true
			if name == target {
				if found != nil {
					return nil, nameSearchError(op)
				}
				item := out.Items[i]
				found = &item
			}
		}
		if total == 0 {
			if len(seen) != 0 || pages > 1 {
				return nil, nameSearchError(op)
			}
			return nil, nil
		}
		if pages == 0 || len(out.Items) == 0 || len(seen) > total {
			return nil, nameSearchError(op)
		}
		if page-firstPage+1 == pages {
			if len(seen) != total {
				return nil, nameSearchError(op)
			}
			return found, nil
		}
	}
	return nil, nameSearchError(op)
}
