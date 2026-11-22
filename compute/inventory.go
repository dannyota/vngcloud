package compute

import (
	"bytes"
	"context"
	"encoding/json"

	"danny.vn/vngcloud/internal/core"
)

func validComputePage(raw []byte) bool {
	var meta struct {
		ListData  json.RawMessage `json:"listData"`
		Page      *int            `json:"page"`
		PageSize  *int            `json:"pageSize"`
		TotalPage *int            `json:"totalPage"`
		TotalItem *int            `json:"totalItem"`
	}
	if json.Unmarshal(raw, &meta) != nil || !bytes.HasPrefix(bytes.TrimSpace(meta.ListData), []byte("[")) || meta.Page == nil || meta.PageSize == nil || meta.TotalPage == nil || meta.TotalItem == nil {
		return false
	}
	var items []json.RawMessage
	return json.Unmarshal(meta.ListData, &items) == nil && (*meta.Page > 0 || (*meta.Page == 0 && *meta.TotalPage == 0 && *meta.TotalItem == 0)) && *meta.PageSize > 0 && *meta.TotalPage >= 0 && *meta.TotalItem >= len(items) && len(items) <= *meta.PageSize && (len(items) == 0 || *meta.TotalPage >= *meta.Page) && (*meta.TotalItem != 0 || *meta.TotalPage <= 1)
}

func walkComputePages[T any](ctx context.Context, op string, fetch func(int) (*core.PagedList[T], error), identity func(T) string) ([]T, error) {
	var items []T
	seen := make(map[string]bool)
	pages, total, size := 0, 0, 0
	for page := 1; page <= 50; page++ {
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
		if out == nil || out.Page != page || out.PageSize <= 0 || out.TotalPage < 0 || out.TotalPage > 50 || out.TotalItem < 0 || out.TotalItem > 500000 || len(out.Items) > out.PageSize || (out.TotalItem > 0 && (out.TotalPage == 0 || len(out.Items) == 0)) {
			return nil, invalidServerResponse(op, 200, nil)
		}
		if page == 1 {
			pages, total, size = out.TotalPage, out.TotalItem, out.PageSize
		}
		if pages != out.TotalPage || total != out.TotalItem || size != out.PageSize {
			return nil, invalidServerResponse(op, 200, nil)
		}
		for _, item := range out.Items {
			id := identity(item)
			if core.CheckPathID(op, "resource ID", id) != nil || seen[id] {
				return nil, invalidServerResponse(op, 200, nil)
			}
			seen[id] = true
		}
		items = append(items, out.Items...)
		if len(items) > total {
			return nil, invalidServerResponse(op, 200, nil)
		}
		if page >= pages {
			if len(items) != total {
				return nil, invalidServerResponse(op, 200, nil)
			}
			return items, nil
		}
	}
	return nil, invalidServerResponse(op, 200, nil)
}

func (c *Client) allServerGroups(ctx context.Context) ([]ServerGroup, error) {
	offset := 0
	return walkComputePages(ctx, "compute.ListServerGroups", func(int) (*ListServerGroupsOutput, error) {
		out, err := c.ListServerGroups(ctx, &ListServerGroupsInput{Offset: offset})
		if out != nil {
			offset += len(out.Items)
		}
		return out, err
	}, func(group ServerGroup) string { return group.UUID })
}
