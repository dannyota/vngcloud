// Package containerregistry lists, creates, and deletes vContainer Registry
// repositories, and lists, creates, and deletes repository users.
package containerregistry

import (
	"context"
	"encoding/json"
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
	name, page, size := "", 0, 0
	if in != nil {
		name, page, size = in.Name, in.Page, in.Size
	}
	q := listUsersQuery(name, page, size)
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

// ListRepositoryUsersInput identifies the repository whose users are listed.
type ListRepositoryUsersInput struct {
	RepositoryID string `vngcloud:"required"`
	Name         string
	Page         int
	Size         int
}

type ListRepositoryUsersOutput = core.PagedList[User]

// ListRepositoryUsers lists the users attached to a repository.
func (c *Client) ListRepositoryUsers(ctx context.Context, in *ListRepositoryUsersInput) (*ListRepositoryUsersOutput, error) {
	const op = "containerregistry.ListRepositoryUsers"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RepositoryID", in.RepositoryID); err != nil {
		return nil, err
	}

	q := listUsersQuery(in.Name, in.Page, in.Size)
	var resp listUsersResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    "GET",
		URL:       c.vcrURL([]string{"repository", in.RepositoryID, "user"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

// ListPermissionsInput takes no fields: the permission list is not scoped by
// repository or user.
type ListPermissionsInput struct{}

type ListPermissionsOutput = core.List[Permission]

// ListPermissions reads every action the server accepts in a user's
// permission list, each paired with the policy id CreateUser sends for it.
// The response is a bare JSON array, not the "listData"/"data" envelope
// ListRepositories and ListUsers share, so it decodes straight into
// []Permission.
func (c *Client) ListPermissions(ctx context.Context, _ *ListPermissionsInput) (*ListPermissionsOutput, error) {
	var items []Permission
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "containerregistry.ListPermissions",
		Method:    "GET",
		URL:       c.vcrURL([]string{"user", "permissions"}, nil),
		OK:        []int{200},
	}, &items); err != nil {
		return nil, err
	}
	return &ListPermissionsOutput{Items: items}, nil
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

// listUsersQuery builds the name/page/size query ListUsers and
// ListRepositoryUsers share, defaulting page and size the way every other
// paged list in this package does when the caller leaves them at 0.
func listUsersQuery(name string, page, size int) url.Values {
	if page <= 0 {
		page = core.DefaultPage
	}
	if size <= 0 {
		size = core.DefaultPageSize
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

	// imageCountKnown records whether the decoded response carried a
	// non-null imageCount. DeleteRepository's pre-delete guard reads it
	// through imageCountIsKnown to refuse the delete when the count is
	// unknown, rather than defaulting ImageCount to 0 and failing open.
	// Unexported: encoding/json never touches it either way.
	imageCountKnown bool
}

// imageCountIsKnown reports whether r's ImageCount came from a non-null
// imageCount in the decoded response.
func (r Repository) imageCountIsKnown() bool {
	return r.imageCountKnown
}

// UnmarshalJSON decodes a bare RepositoryDto body, the shape every
// repository create, get, delete, and list row shares (see Repository). It
// reads imageCount into a pointer first so a missing or explicit null value
// is detectable: see imageCountKnown.
func (r *Repository) UnmarshalJSON(data []byte) error {
	type repositoryAlias Repository
	aux := struct {
		ImageCount *int `json:"imageCount"`
		*repositoryAlias
	}{repositoryAlias: (*repositoryAlias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.imageCountKnown = aux.ImageCount != nil
	if aux.ImageCount != nil {
		r.ImageCount = *aux.ImageCount
	} else {
		r.ImageCount = 0
	}
	return nil
}

// User is a vCR repository user (a "robot account" in the reference).
// Fields follow a live GET user/{id} list row (RobotAccountDto), the shape
// ListUsers and ListRepositoryUsers rows and CreateUser's own lookup read
// share. UserID (the "userId" key) is a separate value from ID: ID is the
// robot account's own id, the one DeleteUserInput.UserID and a repository
// permission's own RepositoryID pattern of use take, formatted "ra-<uuid>"
// per the reference; UserID's own relation to the account has not been
// confirmed by a live capture.
type User struct {
	ID                   string                 `json:"uuid"`
	Name                 string                 `json:"name"`
	BackendName          string                 `json:"backendName"`
	Description          string                 `json:"description"`
	Disabled             bool                   `json:"disable"`
	ExpiredAt            string                 `json:"expiredAt"`
	CreatedAt            string                 `json:"createdAt"`
	NumberOfRepositories int                    `json:"numberOfRepo"`
	UserID               string                 `json:"userId"`
	Repositories         []RepositoryPermission `json:"repoPermissionList"`
}

// RepositoryPermission is one repository a User can act on, and the
// policies it grants there.
type RepositoryPermission struct {
	RepositoryID          string       `json:"repoId"`
	RepositoryName        string       `json:"repoName"`
	BackendRepositoryName string       `json:"backendRepoName"`
	Policies              []Permission `json:"policyDtoList"`
}

// Permission is one action a repository user can be granted, and the
// policy id CreateUser sends for it. ListPermissions returns the server's
// whole list; a RepositoryPermission's own Policies field reuses the same
// shape for the policies a particular user already holds.
type Permission struct {
	ID     string `json:"uuid"`
	Action string `json:"action"`
}
