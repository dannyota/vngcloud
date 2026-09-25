// Package project lists the projects visible to the configured account in
// the configured region.
package project

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// Client is the project listing client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

// Project is a VNG Cloud project.
type Project = core.Project

type ListProjectsInput struct {
	Region string
}

type ListProjectsOutput = core.List[Project]

func (c *Client) ListProjects(ctx context.Context, in *ListProjectsInput) (*ListProjectsOutput, error) {
	var opts *core.ListProjectsOptions
	if in != nil {
		opts = &core.ListProjectsOptions{Region: in.Region}
	}
	items, err := c.c.ListProjects(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &ListProjectsOutput{Items: items}, nil
}
