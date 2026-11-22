package volume

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
)

const maxSnapshotPages = 50

func incompleteSnapshots() error {
	return &core.APIError{Operation: "volume.ListAllSnapshots", StatusCode: 200, Code: "InvalidResponse", Message: "snapshot inventory is incomplete or invalid; body withheld"}
}

func decodeInventoryPage(op string, raw json.RawMessage, target any, list, pages, total string) error {
	if jsonresponse.ValidateStrict(raw, target) != nil || json.Unmarshal(raw, target) != nil {
		return &core.APIError{Operation: op, StatusCode: 200, Code: "InvalidResponse", Message: "page fields are missing, conflicting, or invalid; body withheld"}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return &core.APIError{Operation: op, StatusCode: 200, Code: "InvalidResponse", Message: "page fields are missing, conflicting, or invalid; body withheld"}
	}
	for _, required := range []string{list, "page", "pageSize", pages, total} {
		present := false
		for key, value := range fields {
			if strings.EqualFold(key, required) && string(value) != "null" {
				present = true
			}
		}
		if !present {
			return &core.APIError{Operation: op, StatusCode: 200, Code: "InvalidResponse", Message: "page fields are missing, conflicting, or invalid; body withheld"}
		}
	}
	return nil
}

func snapshotPages[T any](ctx context.Context, identity func(T) (string, error), fetch func(int) (*core.PagedList[T], error)) ([]T, error) {
	items := []T{}
	seen := map[string]bool{}
	pages, total, size := 0, 0, 0
	for page := 1; page <= maxSnapshotPages; page++ {
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
		if out == nil || out.Items == nil || out.TotalPage < 0 || out.TotalPage > maxSnapshotPages || out.TotalItem < 0 || out.PageSize <= 0 || len(out.Items) > out.PageSize {
			return nil, incompleteSnapshots()
		}
		if page == 1 && out.TotalItem == 0 && len(out.Items) == 0 && out.TotalPage <= 1 && (out.Page == 0 || out.Page == 1) {
			return items, nil
		}
		if out.Page != page || out.TotalPage < page || len(out.Items) == 0 {
			return nil, incompleteSnapshots()
		}
		if page == 1 {
			pages, total, size = out.TotalPage, out.TotalItem, out.PageSize
		} else if out.TotalPage != pages || out.TotalItem != total || out.PageSize != size {
			return nil, incompleteSnapshots()
		}
		for _, item := range out.Items {
			id, err := identity(item)
			if err != nil || core.CheckPathID("volume.ListAllSnapshots", "ID", id) != nil || seen[id] {
				return nil, incompleteSnapshots()
			}
			seen[id] = true
			items = append(items, item)
		}
		if len(items) > total {
			return nil, incompleteSnapshots()
		}
		if page == pages {
			if len(items) != total {
				return nil, incompleteSnapshots()
			}
			return items, nil
		}
	}
	return nil, incompleteSnapshots()
}

func (c *Client) allSnapshots(ctx context.Context) (*ListAllSnapshotsOutput, error) {
	volumes, err := snapshotPages(ctx, func(v Volume) (string, error) { return v.UUID, nil }, func(page int) (*ListVolumesOutput, error) {
		return c.ListVolumes(ctx, &ListVolumesInput{Page: page})
	})
	if err != nil {
		return nil, err
	}
	items := []Snapshot{}
	seen := map[string]bool{}
	for _, vol := range volumes {
		snapshots, err := snapshotPages(ctx, func(s Snapshot) (string, error) {
			if s.VolumeID != vol.UUID {
				return "", incompleteSnapshots()
			}
			return cmp.Or(s.ID, s.UUID), nil
		}, func(page int) (*ListSnapshotsOutput, error) {
			return c.ListSnapshots(ctx, &ListSnapshotsInput{VolumeID: vol.UUID, Page: page})
		})
		if err != nil {
			return nil, err
		}
		for _, s := range snapshots {
			id := cmp.Or(s.ID, s.UUID)
			if seen[id] {
				return nil, incompleteSnapshots()
			}
			seen[id] = true
			items = append(items, s)
		}
	}
	return &ListAllSnapshotsOutput{Items: items}, nil
}
