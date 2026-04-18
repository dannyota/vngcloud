package containerregistry

import (
	"context"
	"net/url"
	"strconv"

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

type ListContainerRepositoriesOptions struct {
	AccessLevel string
}

type ListContainerRegistryUsersOptions struct {
	Name string
	Page int
	Size int
}

type ListContainerRepositoriesResult = core.ListResult[ContainerRepository]
type ListContainerRegistryUsersResult = core.ListResult[ContainerRegistryUser]

func (s *Service) ListRepositories(ctx context.Context, opts *ListContainerRepositoriesOptions) (*ListContainerRepositoriesResult, error) {
	q := url.Values{}
	accessLevel := "ALL"
	if opts != nil && opts.AccessLevel != "" {
		accessLevel = opts.AccessLevel
	}
	q.Set("accessLevel", accessLevel)

	var resp listContainerRepositoriesResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "VCR.ListRepositories",
		Method:    "GET",
		URL:       s.vcrURL("v1", []string{"repository"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.Items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) ListUsers(ctx context.Context, opts *ListContainerRegistryUsersOptions) (*ListContainerRegistryUsersResult, error) {
	q := containerRegistryUserListQuery(opts)
	var resp listContainerRegistryUsersResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "VCR.ListUsers",
		Method:    "GET",
		URL:       s.vcrURL("v1", []string{"user"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.Items, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) vcrURL(version string, parts []string, q url.Values) string {
	return s.client.RouteURL(routes.Route{
		Product: routes.ProductVCR,
		Version: version,
		Parts:   parts,
		Query:   q,
	})
}

func containerRegistryUserListQuery(opts *ListContainerRegistryUsersOptions) url.Values {
	page, size := core.DefaultPage, core.DefaultPageSize
	name := ""
	if opts != nil {
		name = opts.Name
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.Size > 0 {
			size = opts.Size
		}
	}
	q := url.Values{}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}

type listContainerRepositoriesResponse struct {
	Items     []ContainerRepository
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

type listContainerRegistryUsersResponse struct {
	Items     []ContainerRegistryUser
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

func (r *listContainerRepositoriesResponse) UnmarshalJSON(data []byte) error {
	items, page, pageSize, totalPage, totalItem, err := core.DecodeFlexibleList[ContainerRepository](data)
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

func (r *listContainerRegistryUsersResponse) UnmarshalJSON(data []byte) error {
	items, page, pageSize, totalPage, totalItem, err := core.DecodeFlexibleList[ContainerRegistryUser](data)
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

// ContainerRepository is map-backed until live rows are available to type the
// model without dropping fields.
type ContainerRepository map[string]any

// ContainerRegistryUser is map-backed until live rows are available to type the
// model without dropping fields.
type ContainerRegistryUser map[string]any
