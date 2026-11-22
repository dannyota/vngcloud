package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
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
	env, err := c.exchange(ctx, call{
		op: op, method: http.MethodGet, regionID: id, ok: []int{http.StatusOK}, noRedirect: op == "storage.DeleteProject",
		url: c.route([]string{"projects"}, nil),
		validate: func(raw json.RawMessage) error {
			if jsonresponse.ValidateStrict(raw, (*envelope)(nil)) != nil {
				return projectResponseError(op, "list is null or malformed")
			}
			if op == "storage.DeleteProject" {
				if err := validateDeletionEvidence(op, raw, true, ""); err != nil {
					return err
				}
			}
			return validateProjectPagination(op, raw)
		},
	})
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
		if jsonresponse.ValidateStrict(item, &p) != nil || json.Unmarshal(item, &p) != nil || json.Unmarshal(item, &fields) != nil || core.CheckPathID(op, "ProjectID", p.ID) != nil || p.Name == "" || p.RegionID != id || seen[p.ID] {
			return nil, projectResponseError(op, "project list has invalid or duplicate identity")
		}
		seen[p.ID] = true
		result = append(result, projectRecord{project: p, fields: fields})
	}
	return result, nil
}

func validateDeletionEvidence(op string, raw json.RawMessage, projects bool, bucket string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return projectResponseError(op, "list has invalid envelope")
	}
	for key := range fields {
		for i := 0; i < len(key); i++ {
			if key[i] >= 0x80 {
				return projectResponseError(op, "list has unsupported envelope field")
			}
		}
	}
	var status struct {
		Success *bool           `json:"success"`
		Code    json.RawMessage `json:"code"`
		Data    json.RawMessage `json:"data"`
		IsNext  json.RawMessage `json:"isNext"`
	}
	if json.Unmarshal(raw, &status) != nil || status.Success == nil {
		return projectResponseError(op, "list has invalid envelope")
	}
	if !*status.Success {
		return nil
	}
	if len(status.Code) != 0 && string(status.Code) != "200" {
		return projectResponseError(op, "response has inconsistent success code")
	}
	if len(status.IsNext) != 0 && string(status.IsNext) != "false" {
		return projectResponseError(op, "list has incomplete or invalid pagination")
	}
	for key := range fields {
		switch strings.ToLower(key) {
		case "code", "success", "data", "isnext":
		case "datas":
			if bucket != "" {
				return projectResponseError(op, "response has unsupported envelope field")
			}
		case "action":
			if !projects {
				return projectResponseError(op, "list has unsupported envelope field")
			}
		default:
			return projectResponseError(op, "list has unsupported envelope field")
		}
	}
	if bucket != "" {
		var usage bucketUsage
		if jsonresponse.ValidateStrict(status.Data, &usage) != nil {
			return projectResponseError(op, "invalid bucket usage")
		}
		if err := json.Unmarshal(status.Data, &usage); err != nil {
			return &core.APIError{Operation: op, StatusCode: http.StatusOK, Err: fmt.Errorf("decode bucket: %w", transport.SanitizeDecodeError(err))}
		}
		if usage.Name != bucket {
			return projectResponseError(op, "bucket identity did not match")
		}
	}
	return nil
}

func validateProjectPagination(op string, raw json.RawMessage) error {
	var page struct {
		IsNext json.RawMessage `json:"isNext"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return projectResponseError(op, "list has invalid pagination")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return projectResponseError(op, "list has invalid pagination")
	}
	for key := range fields {
		switch strings.ToLower(key) {
		case "page", "paging", "pagination", "pagesize", "totalpage", "totalpages", "totalitem", "total", "next", "nextpage", "hasmore", "continuationtoken":
			return projectResponseError(op, "list has unsupported pagination")
		}
	}
	if len(page.IsNext) != 0 {
		var next *bool
		if json.Unmarshal(page.IsNext, &next) != nil || next == nil {
			return projectResponseError(op, "list has invalid pagination")
		}
	}
	return nil
}

func completeProjectList(op string, env *envelope) ([]json.RawMessage, error) {
	if env.Success == nil || !*env.Success {
		return nil, projectResponseError(op, "list has no success envelope")
	}
	if env.IsNext {
		return nil, projectResponseError(op, "list is incomplete (isNext)")
	}
	if len(env.Data) > 0 && len(env.Datas) > 0 {
		return nil, projectResponseError(op, "list has conflicting envelopes")
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

func (c *Client) pollProject(ctx context.Context, bound time.Duration, step func(context.Context) (bool, error), timeout func() error) error {
	return poll(ctx, c.now, c.sleep, projectPollInterval, bound, step, timeout)
}
