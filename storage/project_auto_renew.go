package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/billingresources"
	"danny.vn/vngcloud/internal/core"
)

type GetProjectAutoRenewInput struct {
	ProjectID    string `vngcloud:"required"`
	Region       string
	PeriodMonths *int
}

type GetProjectAutoRenewOutput struct{ State *ProjectAutoRenew }

type PutProjectAutoRenewInput struct {
	ProjectID    string `vngcloud:"required"`
	Region       string
	Enabled      *bool `vngcloud:"required"`
	PeriodMonths *int
	MaxPrice     float64
}

type PutProjectAutoRenewOutput struct {
	State   *ProjectAutoRenew
	Changed bool
}

// ProjectAutoRenew joins regional identity with central billing state.
// NextCharge estimates today's create price, not a promised renewal debit.
type ProjectAutoRenew struct {
	ProjectID           string
	Region              string
	ProjectName         string
	EndBillingTime      int64
	EndTime             time.Time
	RenewType           string
	Enabled             *bool
	PeriodMonths        *int
	QuotePeriodMonths   int
	MonthlyPrice        *float64
	QuotedRenewalCharge *float64
	NextCharge          *float64
	Currency            string
	PriceStatus         string
	PriceErrorCode      string
}

type autoRenewObservation struct {
	state    *ProjectAutoRenew
	project  projectRecord
	resource billingresources.Resource
}

var errAutoRenewDisagreement = errors.New("storage: renewal reads disagree")

func autoRenewDisagreement(op, message string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Message: message, Err: errAutoRenewDisagreement}
}

func validRenewalPeriod(months int) bool {
	return months == 1 || months == 3 || months == 6 || months == 12
}

func checkAutoRenewInput(op, id string, period *int) error {
	if err := core.CheckPathID(op, "ProjectID", id); err != nil {
		return err
	}
	if period != nil && !validRenewalPeriod(*period) {
		return fmt.Errorf("%w: %s: PeriodMonths must be 1, 3, 6, or 12", core.ErrInvalidInput, op)
	}
	return nil
}

// GetProjectAutoRenew returns fresh state even when its price is unavailable.
func (c *Client) GetProjectAutoRenew(ctx context.Context, in *GetProjectAutoRenewInput) (*GetProjectAutoRenewOutput, error) {
	const op = "storage.GetProjectAutoRenew"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkAutoRenewInput(op, in.ProjectID, in.PeriodMonths); err != nil {
		return nil, err
	}
	observation, err := c.readAutoRenew(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	out := &GetProjectAutoRenewOutput{State: observation.state}
	months := autoRenewPreview(out.State, in.PeriodMonths)
	err = c.priceAutoRenew(ctx, observation, months)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return out, err
	}
	return out, nil
}

func autoRenewPreview(state *ProjectAutoRenew, explicit *int) int {
	if explicit != nil {
		return *explicit
	}
	if state.Enabled != nil && *state.Enabled {
		return *state.PeriodMonths
	}
	return 1
}

func (c *Client) readAutoRenew(ctx context.Context, op, region, projectID string) (*autoRenewObservation, error) {
	id, err := c.regionID(ctx, op, region)
	if err != nil {
		return nil, err
	}
	// Conflicting list keys cannot prove absence or provide a write guard.
	env, err := c.do(ctx, "storage.ListProjects", c.route([]string{"projects"}, nil), id)
	if err != nil {
		return nil, err
	}
	if len(env.Data) > 0 && len(env.Datas) > 0 {
		var a, b any
		if json.Unmarshal(env.Data, &a) != nil || json.Unmarshal(env.Datas, &b) != nil {
			return nil, projectResponseError(op, "project list is malformed")
		}
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		if string(left) != string(right) {
			return nil, projectResponseError(op, "project list fields conflict")
		}
	}
	raw, err := completeProjectList(op, env)
	if err != nil {
		return nil, err
	}
	var found *projectRecord
	seen := map[string]bool{}
	for _, item := range raw {
		var p Project
		var fields map[string]json.RawMessage
		if json.Unmarshal(item, &p) != nil || json.Unmarshal(item, &fields) != nil || core.CheckPathID(op, "ProjectID", p.ID) != nil || p.Name == "" || p.RegionID != id || seen[p.ID] {
			return nil, projectResponseError(op, "project list has invalid or duplicate identity")
		}
		seen[p.ID] = true
		if p.ID == projectID {
			found = &projectRecord{project: p, fields: fields}
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %s: project absent from complete regional list", core.ErrNotFound, op)
	}
	data, err := billingresources.List(ctx, c.c)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []billingresources.Resource `json:"data"`
	}
	if json.Unmarshal(data, &list) != nil {
		return nil, projectResponseError(op, "billing resource fields have invalid types")
	}
	var resource *billingresources.Resource
	for i := range list.Items {
		r := &list.Items[i]
		if r.Product == "vstorage" && r.ArtifactType == "object-storage" && r.ArtifactID == projectID {
			if resource != nil {
				return nil, projectResponseError(op, "duplicate billing resource identity")
			}
			resource = r
		}
	}
	if resource == nil {
		return nil, projectResponseError(op, "project has no matching billing resource")
	}
	if resource.EndBillingTime == nil {
		return nil, projectResponseError(op, "billing resource has no end timestamp")
	}
	end := time.UnixMilli(*resource.EndBillingTime).UTC()
	if end.Year() < 1 || end.Year() > 9999 {
		return nil, projectResponseError(op, "billing end timestamp is outside RFC3339 range")
	}
	p := found.project
	if p.RegionName == "" || p.EnableAutoRenew == nil {
		return nil, projectResponseError(op, "project has no region name or explicit renewal state")
	}
	expected := region
	if expected == "" {
		switch c.c.Region() {
		case "hcm-3":
			expected = "HCM04"
		case "han-1":
			expected = "HAN02"
		}
	}
	if !strings.EqualFold(p.RegionName, expected) {
		return nil, projectResponseError(op, "project region identity conflicts")
	}
	state := &ProjectAutoRenew{ProjectID: projectID, Region: p.RegionName, ProjectName: p.Name, EndBillingTime: *resource.EndBillingTime, EndTime: end, RenewType: resource.RenewType, PriceStatus: "Unavailable", PriceErrorCode: "NotRequested"}
	switch resource.RenewType {
	case "MANUAL":
		if *p.EnableAutoRenew || resource.RenewPeriod != nil || (p.AutoRenewPeriod != nil && *p.AutoRenewPeriod != 0) {
			return nil, autoRenewDisagreement(op, "manual renewal reads conflict")
		}
		state.Enabled = vngcloud.Ptr(false)
	case "AUTO-RENEW":
		if !*p.EnableAutoRenew || resource.RenewPeriod == nil || *resource.RenewPeriod <= 0 || p.AutoRenewPeriod == nil || *p.AutoRenewPeriod <= 0 || int64(*p.AutoRenewPeriod) != *resource.RenewPeriod {
			return nil, autoRenewDisagreement(op, "enabled renewal reads conflict")
		}
		state.Enabled = vngcloud.Ptr(true)
		months := *p.AutoRenewPeriod
		state.PeriodMonths = &months
	}
	state.QuotePeriodMonths = autoRenewPreview(state, nil)
	return &autoRenewObservation{state: state, project: *found, resource: *resource}, nil
}

func autoRenewInput(o *autoRenewObservation) (*CreateProjectInput, error) {
	const op = "storage.QuoteCreateProject"
	p := o.project.project
	if p.Period != 0 {
		return nil, fmt.Errorf("%w: %s: fixed-period projects are unsupported", core.ErrInvalidInput, op)
	}
	for _, key := range []string{"projectType", "projectTypeName", "purchaseTypeId", "purchaseTypeName", "totalQuota"} {
		raw := o.project.fields[key]
		if len(raw) == 0 || string(raw) == "null" {
			return nil, projectResponseError(op, "project price identity is incomplete")
		}
	}
	// Exact decimal arithmetic prevents a float64 from rounding fractional GB.
	raw := o.project.fields["totalQuota"]
	if len(raw) > 128 {
		return nil, projectResponseError(op, "quota is not exact integral GB")
	}
	quota, ok := new(big.Rat).SetString(string(raw))
	if !ok || !quota.IsInt() || !quota.Num().IsInt64() || quota.Sign() <= 0 {
		return nil, projectResponseError(op, "quota is not positive integral GB")
	}
	return &CreateProjectInput{Region: p.RegionName, Type: p.ProjectTypeName, QuotaGB: quota.Num().Int64()}, nil
}

func priceFailureCode(err error) string {
	switch {
	case errors.Is(err, core.ErrUnpriced):
		return "Unpriced"
	case errors.Is(err, core.ErrInvalidInput):
		return "InvalidInput"
	case errors.Is(err, core.ErrAuth):
		return "Unauthorized"
	case errors.Is(err, core.ErrPermission):
		return "Forbidden"
	case errors.Is(err, core.ErrRateLimited):
		return "Throttled"
	case errors.Is(err, context.Canceled):
		return "Canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "Timeout"
	default:
		return "QuoteFailed"
	}
}

func (c *Client) priceAutoRenew(ctx context.Context, o *autoRenewObservation, months int) error {
	s := o.state
	s.QuotePeriodMonths = months
	s.MonthlyPrice = nil
	s.QuotedRenewalCharge = nil
	s.NextCharge = nil
	s.Currency = ""
	s.PriceStatus = "Unavailable"
	input, err := autoRenewInput(o)
	var quote *QuoteCreateProjectOutput
	if err == nil {
		quote, err = c.quoteCreateProject(ctx, input, &o.project.project)
	}
	if err == nil {
		total := quote.MonthlyPrice * float64(months)
		next := total
		if s.Enabled != nil && *s.Enabled {
			next = quote.MonthlyPrice * float64(*s.PeriodMonths)
		}
		if !finitePositive(total) || !finitePositive(next) {
			err = projectResponseError("storage.QuoteCreateProject", "renewal estimate is not finite and positive")
		} else {
			s.MonthlyPrice = vngcloud.Ptr(quote.MonthlyPrice)
			s.QuotedRenewalCharge = &total
			if s.Enabled != nil && *s.Enabled {
				s.NextCharge = &next
			}
			s.Currency = "VND"
			s.PriceStatus = "Quoted"
			s.PriceErrorCode = ""
		}
	}
	if err != nil {
		s.PriceErrorCode = priceFailureCode(err)
	}
	return err
}

func finitePositive(n float64) bool { return n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
