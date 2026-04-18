package core

import (
	"context"
	"errors"
	"fmt"

	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type Project struct {
	ID        string `json:"projectId"`
	Name      string `json:"name,omitempty"`
	Region    string `json:"region,omitempty"`
	Status    string `json:"status,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
	UserID    int    `json:"userId,omitempty"`
}

type ListProjectsOptions struct {
	Region string
}

type listProjectsResponse struct {
	Projects []Project `json:"projects"`
}

func (c *Client) ListProjects(ctx context.Context, opts *ListProjectsOptions) ([]Project, error) {
	region := c.region
	if opts != nil && opts.Region != "" {
		region = opts.Region
	}

	var resp listProjectsResponse
	err := c.DoJSON(ctx, transport.Request{
		Operation: "ListProjects",
		Method:    "GET",
		URL: c.RouteURL(routes.Route{
			Product: routes.ProductVServer,
			Version: "v1",
			Parts:   []string{"projects"},
		}),
		OK: []int{200},
	}, &resp)
	if err != nil {
		return nil, err
	}

	projects := make([]Project, 0, len(resp.Projects))
	for _, project := range resp.Projects {
		if project.Region == "" {
			project.Region = region
		}
		if region == "" || project.Region == "" || project.Region == region {
			projects = append(projects, project)
		}
	}
	return projects, nil
}

func (c *Client) discoverProject(ctx context.Context) error {
	projects, err := c.ListProjects(ctx, nil)
	if err != nil {
		return err
	}
	switch len(projects) {
	case 0:
		return fmt.Errorf("%w: %s", ErrProjectNotFound, c.region)
	case 1:
		c.projectID = projects[0].ID
		c.projectUserID = projects[0].UserID
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrProjectAmbiguous, c.region)
	}
}

func IsProjectNotFound(err error) bool {
	return errors.Is(err, ErrProjectNotFound)
}

func IsProjectAmbiguous(err error) bool {
	return errors.Is(err, ErrProjectAmbiguous)
}
