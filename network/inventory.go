package network

import (
	"bytes"
	"context"
	"encoding/json"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/floatingip"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

func invalidInventory(op string) error {
	return &core.APIError{Operation: op, Code: "InvalidResponse", Message: "invalid or incomplete inventory; body withheld"}
}

func (c *Client) readNetworkEvidence(ctx context.Context, req transport.Request, out any, required ...string) (err error) {
	ctx, unsafe, cancel := floatingip.ResponseContext(ctx)
	defer cancel()
	defer func() { values, _ := floatingip.Credentials(ctx); err = sanitizeServerError(err, values) }()
	var raw json.RawMessage
	if err := floatingip.ReadJSON(ctx, c.c, req, &raw, unsafe); err != nil {
		return err
	}
	if jsonresponse.ValidateStrict(raw, out) != nil || json.Unmarshal(raw, out) != nil {
		return invalidInventory(req.Operation)
	}
	if len(required) > 0 {
		var envelope struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Data == nil {
			return invalidInventory(req.Operation)
		}
		for _, key := range required {
			value := bytes.TrimSpace(envelope.Data[key])
			if len(value) == 0 || bytes.Equal(value, []byte("null")) {
				return invalidInventory(req.Operation)
			}
		}
	}
	return nil
}

func (c *Client) readInventoryPage(ctx context.Context, req transport.Request, out any) (err error) {
	ctx, unsafe, cancel := floatingip.ResponseContext(ctx)
	defer cancel()
	defer func() { values, _ := floatingip.Credentials(ctx); err = sanitizeServerError(err, values) }()
	var raw json.RawMessage
	if err := floatingip.ReadJSON(ctx, c.c, req, &raw, unsafe); err != nil {
		return err
	}
	var meta struct {
		ListData  json.RawMessage `json:"listData"`
		Page      *int            `json:"page"`
		PageSize  *int            `json:"pageSize"`
		TotalPage *int            `json:"totalPage"`
		TotalItem *int            `json:"totalItem"`
	}
	if jsonresponse.ValidateStrict(raw, out) != nil || json.Unmarshal(raw, &meta) != nil || !bytes.HasPrefix(bytes.TrimSpace(meta.ListData), []byte("[")) || meta.Page == nil || meta.PageSize == nil || meta.TotalPage == nil || meta.TotalItem == nil {
		return invalidInventory(req.Operation)
	}
	var items []json.RawMessage
	if json.Unmarshal(meta.ListData, &items) != nil || *meta.Page < 1 || *meta.PageSize < 1 || *meta.TotalPage < 0 || *meta.TotalItem < len(items) || len(items) > *meta.PageSize || (len(items) > 0 && (*meta.TotalPage < *meta.Page || *meta.TotalPage == 0)) || (*meta.TotalItem == 0 && *meta.TotalPage > 1) {
		return invalidInventory(req.Operation)
	}
	if json.Unmarshal(raw, out) != nil {
		return invalidInventory(req.Operation)
	}
	return nil
}

func walkNetworkPages[T any](ctx context.Context, op string, fetch func(int) (*core.PagedList[T], error), identity func(T) string) ([]T, error) {
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
			return nil, invalidInventory(op)
		}
		if page == 1 {
			pages, total, size = out.TotalPage, out.TotalItem, out.PageSize
		}
		if pages != out.TotalPage || total != out.TotalItem || size != out.PageSize {
			return nil, invalidInventory(op)
		}
		for _, item := range out.Items {
			id := identity(item)
			if core.CheckPathID(op, "resource ID", id) != nil || seen[id] {
				return nil, invalidInventory(op)
			}
			seen[id] = true
		}
		items = append(items, out.Items...)
		if len(items) > total {
			return nil, invalidInventory(op)
		}
		if page >= pages {
			if len(items) != total {
				return nil, invalidInventory(op)
			}
			return items, nil
		}
	}
	return nil, invalidInventory(op)
}
