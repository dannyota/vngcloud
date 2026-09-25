// Package portal reads the VNG Cloud portal's user info, zones, and quotas.
package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the portal service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

type GetUserInfoInput struct{}

type GetUserInfoOutput struct {
	UserInfo UserInfo
}

func (c *Client) GetUserInfo(ctx context.Context, _ *GetUserInfoInput) (*GetUserInfoOutput, error) {
	var resp flexibleObject[UserInfo]
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "portal.GetUserInfo",
		Method:    "GET",
		URL:       c.portalURL("v1", []string{"users", "info"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetUserInfoOutput{UserInfo: resp.Value}, nil
}

type ListZonesInput struct{}

type ListZonesOutput = core.List[Zone]

func (c *Client) ListZones(ctx context.Context, _ *ListZonesInput) (*ListZonesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp flexibleList[Zone]
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "portal.ListZones",
		Method:    "GET",
		URL:       c.vserverURL("v1", []string{projectID, "zones"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListZonesOutput{Items: resp.Items}, nil
}

type ListQuotaUsedInput struct{}

type ListQuotaUsedOutput = core.List[Quota]

func (c *Client) ListQuotaUsed(ctx context.Context, _ *ListQuotaUsedInput) (*ListQuotaUsedOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp flexibleList[Quota]
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "portal.ListQuotaUsed",
		Method:    "GET",
		URL:       c.vserverURL("v2", []string{projectID, "quotas", "quotaUsed"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListQuotaUsedOutput{Items: resp.Items}, nil
}

type GetQuotaInput struct {
	Name string `vngcloud:"required"`
}

type GetQuotaOutput struct {
	Quota Quota
}

func (c *Client) GetQuota(ctx context.Context, in *GetQuotaInput) (*GetQuotaOutput, error) {
	if err := core.CheckRequired("portal.GetQuota", in); err != nil {
		return nil, err
	}
	quotas, err := c.ListQuotaUsed(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, quota := range quotas.Items {
		if quotaMatchesName(quota, in.Name) {
			return &GetQuotaOutput{Quota: quota}, nil
		}
	}
	return nil, fmt.Errorf("%w: quota %s", core.ErrNotFound, in.Name)
}

type GetTagQuotaInput struct{}

type GetTagQuotaOutput struct {
	TagQuota TagQuota
}

func (c *Client) GetTagQuota(ctx context.Context, _ *GetTagQuotaInput) (*GetTagQuotaOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp flexibleObject[TagQuota]
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "portal.GetTagQuota",
		Method:    "GET",
		URL:       c.vserverURL("v2", []string{projectID, "tag", "quota"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetTagQuotaOutput{TagQuota: resp.Value}, nil
}

func quotaMatchesName(quota Quota, name string) bool {
	for _, key := range []string{"name", "quotaName", "resourceName", "resource", "key", "type"} {
		value, ok := quota[key]
		if !ok {
			continue
		}
		if strings.EqualFold(fmt.Sprint(value), name) {
			return true
		}
	}
	return false
}

func (c *Client) portalURL(version string, parts []string) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductPortal,
		Version: version,
		Parts:   parts,
	})
}

func (c *Client) vserverURL(version string, parts []string) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductVServer,
		Version: version,
		Parts:   parts,
	})
}

type flexibleObject[T ~map[string]any] struct {
	Value T
}

func (r *flexibleObject[T]) UnmarshalJSON(data []byte) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"data", "user", "userInfo", "quota"} {
			if nested, ok := object[key].(map[string]any); ok {
				r.Value = T(nested)
				return nil
			}
		}
		r.Value = T(object)
	}
	return nil
}

type flexibleList[T any] struct {
	Items []T
}

func (r *flexibleList[T]) UnmarshalJSON(data []byte) error {
	items, _, _, _, _, err := core.DecodeFlexibleList[T](data)
	if err != nil {
		return err
	}
	r.Items = items
	return nil
}

// UserInfo is map-backed until sanitized live fixtures are available.
type UserInfo map[string]any

// Zone is map-backed until sanitized live fixtures are available.
type Zone map[string]any

// Quota is map-backed until sanitized live fixtures are available.
type Quota map[string]any

// TagQuota is map-backed until sanitized live fixtures are available.
type TagQuota map[string]any
