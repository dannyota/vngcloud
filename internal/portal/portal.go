package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type Service struct {
	client *core.Client
}

func New(client *core.Client) *Service {
	return &Service{client: client}
}

func (s *Service) GetUserInfo(ctx context.Context) (PortalUserInfo, error) {
	var resp flexibleObject[PortalUserInfo]
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Portal.GetUserInfo",
		Method:    "GET",
		URL:       s.portalURL("v1", []string{"users", "info"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Value, nil
}

func (s *Service) ListZones(ctx context.Context) ([]PortalZone, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp flexibleList[PortalZone]
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Portal.ListZones",
		Method:    "GET",
		URL:       s.vserverURL("v1", []string{projectID, "zones"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (s *Service) ListQuotaUsed(ctx context.Context) ([]PortalQuota, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp flexibleList[PortalQuota]
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Portal.ListQuotaUsed",
		Method:    "GET",
		URL:       s.vserverURL("v2", []string{projectID, "quotas", "quotaUsed"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (s *Service) GetQuota(ctx context.Context, name string) (PortalQuota, error) {
	if name == "" {
		return nil, errors.New("vngcloud: quota name is required")
	}
	quotas, err := s.ListQuotaUsed(ctx)
	if err != nil {
		return nil, err
	}
	for _, quota := range quotas {
		if quotaMatchesName(quota, name) {
			return quota, nil
		}
	}
	return nil, fmt.Errorf("%w: quota %s", core.ErrNotFound, name)
}

func (s *Service) GetTagQuota(ctx context.Context) (PortalTagQuota, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp flexibleObject[PortalTagQuota]
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Portal.GetTagQuota",
		Method:    "GET",
		URL:       s.vserverURL("v2", []string{projectID, "tag", "quota"}),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.Value, nil
}

func quotaMatchesName(quota PortalQuota, name string) bool {
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

func (s *Service) portalURL(version string, parts []string) string {
	return s.client.RouteURL(routes.Route{
		Product: routes.ProductPortal,
		Version: version,
		Parts:   parts,
	})
}

func (s *Service) vserverURL(version string, parts []string) string {
	return s.client.RouteURL(routes.Route{
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

// PortalUserInfo is map-backed until sanitized live fixtures are available.
type PortalUserInfo map[string]any

// PortalZone is map-backed until sanitized live fixtures are available.
type PortalZone map[string]any

// PortalQuota is map-backed until sanitized live fixtures are available.
type PortalQuota map[string]any

// PortalTagQuota is map-backed until sanitized live fixtures are available.
type PortalTagQuota map[string]any
