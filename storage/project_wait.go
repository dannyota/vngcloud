package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
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
	if env.Success == nil || !*env.Success {
		return nil, projectResponseError(op, "list has no success envelope")
	}
	if env.IsNext {
		return nil, projectResponseError(op, "list is incomplete (isNext)")
	}
	// Empty regions omit both list keys. Present fields must still be arrays.
	items := []json.RawMessage{}
	for _, raw := range []json.RawMessage{env.Data, env.Datas} {
		if len(raw) == 0 {
			continue
		}
		var decoded []json.RawMessage
		if json.Unmarshal(raw, &decoded) != nil || decoded == nil {
			return nil, projectResponseError(op, "list is null or malformed")
		}
		items = decoded
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
	// A checkout URL identifies the response shape, never payment or identity.
	var echoed Project
	echoErr := json.Unmarshal(env.Data, &echoed)
	var fields map[string]json.RawMessage
	fieldsErr := json.Unmarshal(env.Data, &fields)
	accepted := echoErr == nil && fieldsErr == nil && (projectRecord{project: echoed, fields: fields}).matches(id, in, spec)
	var redirect string
	_ = json.Unmarshal(fields["redirectUrl"], &redirect)
	checkout := strings.TrimSpace(redirect) != ""
	if !accepted && !checkout {
		// Reconcile once, but an unknown response cannot establish acceptance.
		err := c.pollProject(ctx, projectCreateBound, func(ctx context.Context) (bool, error) {
			_, err := c.completeProjects(ctx, op, id)
			return true, err
		}, func() error { return projectResponseError(op, "project confirmation timed out after 120s") })
		if err == nil {
			err = projectResponseError(op, "order response has no recognized data")
		}
		return out, projectUnsettled(op, err, projectOrderRecovery)
	}
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
			if checkout && !accepted {
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
		if in.NoWait && p.EnableAutoRenew != nil && !*p.EnableAutoRenew {
			return true, nil
		}
		if !accepted || in.NoWait {
			return true, projectResponseError(op, "project readiness or disabled renewal is unconfirmed")
		}
		return false, nil
	}
	err := c.pollProject(ctx, projectCreateBound, step, func() error { return projectResponseError(op, "project readiness timed out after 120s") })
	if err != nil {
		if errors.Is(err, ErrPaymentRequired) {
			return out, err
		}
		return out, projectUnsettled(op, err, projectOrderRecovery)
	}
	return out, nil
}

func (c *Client) waitProjectGone(ctx context.Context, op, id, projectID string) error {
	err := c.pollProject(ctx, projectDeleteBound, func(ctx context.Context) (bool, error) {
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

// Use the injected clock for the bound and a remaining-time context for each
// read. Reject late results even when a transport ignores context cancellation.
func (c *Client) pollProject(ctx context.Context, bound time.Duration, step func(context.Context) (bool, error), timeout func() error) error {
	deadline := c.now().Add(bound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := deadline.Sub(c.now())
		if remaining <= 0 {
			return timeout()
		}
		readCtx, cancel := context.WithTimeout(ctx, remaining)
		done, err := step(readCtx)
		readErr := readCtx.Err()
		cancel()
		if !c.now().Before(deadline) {
			return timeout()
		}
		if readErr != nil {
			return readErr
		}
		if err != nil || done {
			return err
		}
		delay := min(projectPollInterval, deadline.Sub(c.now()))
		if err := c.sleep(ctx, delay); err != nil {
			return err
		}
	}
}
