package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// DoDiscoveryJSONStatus applies response security without changing shared
// transport settings or ordinary reads.
func (c *Client) DoDiscoveryJSONStatus(ctx context.Context, req transport.Request, out any, before func(context.Context) error) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	status, err := c.transport.DoDiscoveryJSONStatus(ctx, req, out, before)
	return status, wrapTransportErr(err)
}

// RequireDiscoveryProjectID guards the auxiliary read before cache commits.
func (c *Client) RequireDiscoveryProjectID(s *discovery.Session) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	ctx := s.Context()
	c.projectMu.Lock()
	defer c.projectMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.projectID != "" {
		return c.projectID, nil
	}
	req := transport.Request{Operation: "project.ListProjects", Method: http.MethodGet, URL: c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v1", Parts: []string{"projects"}}), OK: []int{200}}
	var projects []Project
	_, err := s.DoJSON(c, req, nil, func(status int, raw json.RawMessage) error {
		var err error
		projects, err = decodeDiscoveryProjects(req.Operation, status, raw)
		return err
	})
	if err != nil {
		return "", err
	}
	if err := s.AddRows(len(projects)); err != nil {
		return "", err
	}
	selected := make([]Project, 0, len(projects))
	for _, p := range projects {
		if p.Region == "" {
			p.Region = c.region
		}
		if p.Region == c.region {
			selected = append(selected, p)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch len(selected) {
	case 0:
		return "", fmt.Errorf("%w: %s", ErrProjectNotFound, c.region)
	case 1:
		c.projectID = selected[0].ID
		c.projectUserID = selected[0].UserID
		return c.projectID, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrProjectAmbiguous, c.region)
	}
}

func decodeDiscoveryProjects(op string, status int, raw json.RawMessage) ([]Project, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, discoverySchemaError(op, status)
	}
	list, ok := envelope["projects"]
	if !ok || string(list) == "null" {
		return nil, discoverySchemaError(op, status)
	}
	for _, key := range []string{"page", "pageSize", "paging", "pagination", "totalPage", "totalPages", "next", "nextPage", "hasMore", "continuationToken"} {
		if _, ok := envelope[key]; ok {
			return nil, discoverySchemaError(op, status)
		}
	}
	var projects []Project
	if json.Unmarshal(list, &projects) != nil {
		return nil, discoverySchemaError(op, status)
	}
	seen := make(map[string]bool)
	for _, p := range projects {
		if p.ID == "" || seen[p.ID] {
			return nil, discoverySchemaError(op, status)
		}
		seen[p.ID] = true
	}
	return projects, nil
}

func discoverySchemaError(op string, status int) error {
	return &APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid discovery response; body withheld"}
}

// WrapDiscoveryError maps private transport validation errors to SDK errors.
func (c *Client) WrapDiscoveryError(err error) error { return wrapTransportErr(err) }

// DiscoveryFatal stops scheduling after authentication or throttle exhaustion.
func (c *Client) DiscoveryFatal(err error) bool {
	return errors.Is(err, ErrAuth) || errors.Is(err, ErrRateLimited)
}
