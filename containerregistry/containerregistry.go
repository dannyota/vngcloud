// Package containerregistry lists repositories and users in the vContainer
// Registry.
package containerregistry

import (
	"context"
	"net/url"
	"strconv"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the container registry service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

type ListRepositoriesInput struct {
	AccessLevel string
}

type ListRepositoriesOutput = core.PagedList[Repository]

func (c *Client) ListRepositories(ctx context.Context, in *ListRepositoriesInput) (*ListRepositoriesOutput, error) {
	q := url.Values{}
	accessLevel := "ALL"
	if in != nil && in.AccessLevel != "" {
		accessLevel = in.AccessLevel
	}
	q.Set("accessLevel", accessLevel)

	var resp listRepositoriesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "containerregistry.ListRepositories",
		Method:    "GET",
		URL:       c.vcrURL("v1", []string{"repository"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type ListUsersInput struct {
	Name string
	Page int
	Size int
}

type ListUsersOutput = core.PagedList[User]

func (c *Client) ListUsers(ctx context.Context, in *ListUsersInput) (*ListUsersOutput, error) {
	q := listUsersQuery(in)
	var resp listUsersResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "containerregistry.ListUsers",
		Method:    "GET",
		URL:       c.vcrURL("v1", []string{"user"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (c *Client) vcrURL(version string, parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductVCR,
		Version: version,
		Parts:   parts,
		Query:   q,
	})
}

func listUsersQuery(in *ListUsersInput) url.Values {
	page, size := core.DefaultPage, core.DefaultPageSize
	name := ""
	if in != nil {
		name = in.Name
		if in.Page > 0 {
			page = in.Page
		}
		if in.Size > 0 {
			size = in.Size
		}
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}

type listRepositoriesResponse struct {
	Items     []Repository
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

type listUsersResponse struct {
	Items     []User
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

func (r *listRepositoriesResponse) UnmarshalJSON(data []byte) error {
	items, page, pageSize, totalPage, totalItem, err := core.DecodeFlexibleList[Repository](data)
	if err != nil {
		return err
	}
	r.Items = items
	r.Page = page
	r.PageSize = pageSize
	r.TotalPage = totalPage
	r.TotalItem = totalItem
	return nil
}

func (r *listUsersResponse) UnmarshalJSON(data []byte) error {
	items, page, pageSize, totalPage, totalItem, err := core.DecodeFlexibleList[User](data)
	if err != nil {
		return err
	}
	r.Items = items
	r.Page = page
	r.PageSize = pageSize
	r.TotalPage = totalPage
	r.TotalItem = totalItem
	return nil
}

// Repository is map-backed until live rows are available to type the model
// without dropping fields.
type Repository map[string]any

// User is map-backed until live rows are available to type the model
// without dropping fields.
type User map[string]any
