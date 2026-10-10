package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"danny.vn/vngcloud/internal/core"
)

const (
	projectPollInterval = 2 * time.Second
	projectCreateBound  = 120 * time.Second
	projectDeleteBound  = 60 * time.Second
)

type projectRecord struct {
	project Project
	fields  map[string]json.RawMessage
}

func (c *Client) completeProjects(ctx context.Context, op, id string) ([]projectRecord, error) {
	env, err := c.do(ctx, op, c.route([]string{"projects"}, nil), id)
	if err != nil {
		return nil, err
	}
	raw, err := completeProjectList(op, env)
	if err != nil {
		return nil, err
	}
	result := make([]projectRecord, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		var p Project
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &p) != nil || json.Unmarshal(item, &fields) != nil || core.CheckPathID(op, "ProjectID", p.ID) != nil || p.Name == "" || p.RegionID != id || seen[p.ID] {
			return nil, projectResponseError(op, "project list has invalid or duplicate identity")
		}
		seen[p.ID] = true
		result = append(result, projectRecord{project: p, fields: fields})
	}
	return result, nil
}

func completeProjectList(op string, env *envelope) ([]json.RawMessage, error) {
	if env.IsNext {
		return nil, projectResponseError(op, "list is incomplete (isNext)")
	}
	raw := env.Datas
	if len(raw) == 0 || string(raw) == "null" {
		raw = env.Data
	}
	var items []json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &items) != nil || items == nil {
		return nil, projectResponseError(op, "list is missing, null, or malformed")
	}
	return items, nil
}

func (r projectRecord) matches(id string, in *CreateProjectInput, spec projectPurchaseSpec) bool {
	for _, key := range []string{"projectId", "projectName", "regionId", "projectType", "purchaseTypeId", "totalQuota", "status"} {
		value := r.fields[key]
		if len(value) == 0 || string(value) == "null" {
			return false
		}
	}
	p := r.project
	if core.CheckPathID("storage.CreateProject", "ProjectID", p.ID) != nil || p.Name != in.Name || p.RegionID != id || p.ProjectType != spec.projectTypeID || p.PurchaseTypeID != spec.purchaseTypeID || p.TotalQuota != float64(spec.quota) {
		return false
	}
	// A float64 can round a different quota to the requested integer.
	raw := r.fields["totalQuota"]
	if len(raw) > 128 {
		return false
	}
	quota, ok := new(big.Rat).SetString(string(raw))
	return ok && quota.Cmp(big.NewRat(spec.quota, 1)) == 0
}

func (c *Client) confirmProjectOrder(ctx context.Context, op, id string, in *CreateProjectInput, spec projectPurchaseSpec, before []projectRecord, env *envelope, out *CreateProjectOutput) (*CreateProjectOutput, error) {
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return out, projectUnsettled(op, projectResponseError(op, "order response has no recognized data"), projectOrderRecovery)
	}
	// Only an echoed project identity can support readiness polling. A redirect
	// is never evidence of acceptance, payment, or a project ID.
	var echoed Project
	echoErr := json.Unmarshal(env.Data, &echoed)
	var fields map[string]json.RawMessage
	fieldsErr := json.Unmarshal(env.Data, &fields)
	accepted := echoErr == nil && fieldsErr == nil && (projectRecord{project: echoed, fields: fields}).matches(id, in, spec)
	baseline := map[string]bool{}
	for _, r := range before {
		baseline[r.project.ID] = true
	}
	if echoed.EnableAutoRenew != nil && *echoed.EnableAutoRenew {
		return out, projectUnsettled(op, projectResponseError(op, "order response reports auto-renew enabled"), projectOrderRecovery)
	}
	if accepted && baseline[echoed.ID] {
		return out, projectUnsettled(op, projectResponseError(op, "order response names an existing project"), projectOrderRecovery)
	}
	step := func(ctx context.Context) (bool, error) {
		items, err := c.completeProjects(ctx, op, id)
		if err != nil {
			return true, err
		}
		var found *projectRecord
		for i := range items {
			r := &items[i]
			if r.project.Name != in.Name {
				continue
			}
			if baseline[r.project.ID] || found != nil {
				return true, projectResponseError(op, "project confirmation is ambiguous")
			}
			found = r
		}
		if found == nil {
			if !accepted {
				return true, fmt.Errorf("%w: %s", ErrPaymentRequired, projectPaymentRecovery)
			}
			if in.NoWait {
				return true, nil
			}
			return false, nil
		}
		if !found.matches(id, in, spec) || (accepted && found.project.ID != echoed.ID) {
			return true, projectResponseError(op, "project identity, type, or quota did not match")
		}
		p := found.project
		out.Project = &p
		if p.EnableAutoRenew != nil && *p.EnableAutoRenew {
			return true, projectResponseError(op, "project has auto-renew enabled")
		}
		if p.Status == 1 && p.EnableAutoRenew != nil && !*p.EnableAutoRenew {
			return true, nil
		}
		if !accepted || in.NoWait {
			return true, projectResponseError(op, "project readiness or disabled renewal is unconfirmed")
		}
		return false, nil
	}
	err := poll(ctx, c.now, c.sleep, projectPollInterval, projectCreateBound, step, func() error { return projectResponseError(op, "project readiness timed out after 120s") })
	if err != nil {
		if errors.Is(err, ErrPaymentRequired) {
			return out, err
		}
		return out, projectUnsettled(op, err, projectOrderRecovery)
	}
	return out, nil
}

func (c *Client) waitProjectGone(ctx context.Context, op, id, projectID string) error {
	err := poll(ctx, c.now, c.sleep, projectPollInterval, projectDeleteBound, func(ctx context.Context) (bool, error) {
		items, err := c.completeProjects(ctx, op, id)
		if err != nil {
			return true, err
		}
		for _, r := range items {
			if r.project.ID == projectID {
				return false, nil
			}
		}
		return true, nil
	}, func() error { return projectResponseError(op, "project still present after 60s") })
	if err != nil {
		return projectUnsettled(op, err, projectDeleteRecovery)
	}
	return nil
}
