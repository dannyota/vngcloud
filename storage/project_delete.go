package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"danny.vn/vngcloud/internal/core"
)

// ErrProjectNotEmpty means DeleteProject found at least one bucket.
var ErrProjectNotEmpty = errors.New("storage: project has buckets")

const projectDeleteRecovery = "List projects in the same region and inspect billing before another delete attempt."

type DeleteProjectInput struct {
	Region    string
	ProjectID string `vngcloud:"required"`
	NoWait    bool
}

type DeleteProjectOutput struct{}

// DeleteProject deletes a project only after a complete list proves it exists
// and a complete bucket list proves it has no buckets. Stop bucket writers
// first: the API provides no precondition against concurrent bucket creation.
func (c *Client) DeleteProject(ctx context.Context, in *DeleteProjectInput) (*DeleteProjectOutput, error) {
	const op = "storage.DeleteProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ProjectID", in.ProjectID); err != nil {
		return nil, err
	}
	id, err := c.regionID(ctx, op, in.Region)
	if err != nil {
		return nil, err
	}
	items, err := c.completeProjects(ctx, op, id)
	if err != nil {
		return nil, err
	}
	exists := false
	for _, r := range items {
		if r.project.ID == in.ProjectID {
			exists = true
		}
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s: project absent from complete regional list", core.ErrNotFound, op)
	}
	env, err := c.do(ctx, op, c.route([]string{"ceph", "projects", in.ProjectID}, url.Values{"limit": {listBucketsLimit}}), id)
	if err != nil {
		return nil, err
	}
	buckets, err := completeProjectList(op, env)
	if err != nil {
		return nil, err
	}
	if len(buckets) > 0 {
		return nil, fmt.Errorf("%w: %s: remove all buckets first", ErrProjectNotEmpty, op)
	}
	out := &DeleteProjectOutput{}
	_, err = c.exchangeProjectWrite(ctx, call{op: op, method: http.MethodDelete, url: c.route([]string{"projects", in.ProjectID}, url.Values{"region_id": {id}}), regionID: id, body: struct{}{}, ok: []int{http.StatusOK}, write: true, once: true, sensitive: true})
	if err != nil {
		return out, projectWriteError(op, err, projectDeleteRecovery)
	}
	if in.NoWait {
		return out, nil
	}
	return out, c.waitProjectGone(ctx, op, id, in.ProjectID)
}
