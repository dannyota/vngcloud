// Package containerregistry lists, creates, and deletes vContainer Registry
// repositories, and lists users.
package containerregistry

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the container registry service client.
type Client struct {
	c *core.Client

	// sleep and now back CreateRepository and DeleteRepository's post-write
	// waits; see waitRepositoryConfirmed and waitRepositoryAbsent. Tests
	// replace both with fakes so the real 2-second interval and 60-second
	// bound never really elapse.
	sleep sleepFunc
	now   clockFunc
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg), sleep: contextSleep, now: time.Now}
}

type ListRepositoriesInput struct {
	AccessLevel string
	Name        string
}

type ListRepositoriesOutput = core.PagedList[Repository]

func (c *Client) ListRepositories(ctx context.Context, in *ListRepositoriesInput) (*ListRepositoriesOutput, error) {
	q := url.Values{}
	accessLevel := "ALL"
	name := ""
	if in != nil {
		if in.AccessLevel != "" {
			accessLevel = in.AccessLevel
		}
		name = in.Name
	}
	q.Set("accessLevel", accessLevel)
	q.Set("name", name)

	var resp listRepositoriesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "containerregistry.ListRepositories",
		Method:    "GET",
		URL:       c.vcrURL([]string{"repository"}, q),
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
		URL:       c.vcrURL([]string{"user"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

// vcrURL builds a vCR route. Every call in this package is against v1: the
// reference names no other version.
func (c *Client) vcrURL(parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductVCR,
		Version: "v1",
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

// Repository is a vCR repository. Fields follow a live GET repository/{id}
// body, which is bare (no envelope) and carries exactly these keys; create,
// delete, and list responses share the same shape. There is no status
// field: the API never reports one, so CreateRepository and
// DeleteRepository confirm by reading the repository rather than waiting on
// a status value.
type Repository struct {
	ID            string  `json:"uuid"`
	Name          string  `json:"name"`
	BackendName   string  `json:"backendName"`
	AccessLevel   string  `json:"accessLevel"`
	RegistryURL   string  `json:"registryUrl"`
	QuotaLimitGB  int     `json:"quotaLimit"`
	QuotaUsed     float64 `json:"quotaUsed"`
	ImageCount    int     `json:"imageCount"`
	AttachedUsers int     `json:"attachedUser"`
	CreatedAt     string  `json:"createdAt"`
}

// User is map-backed until live rows are available to type the model
// without dropping fields.
type User map[string]any
