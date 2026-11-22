package pricing

import (
	"context"
	"errors"
	"fmt"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
	"danny.vn/vngcloud/internal/transport"
)

const estimateOperation = "pricing.Estimate"

type estimateRun struct {
	client   *core.Client
	session  *discovery.Session
	input    *EstimateInput
	output   *EstimateOutput
	requests []*transport.Request
	errors   []error
	stopped  bool
	catalogs map[string]estimateCatalogResult
}

type estimateCatalogResult struct {
	value any
	err   error
}

// Estimate returns account-scoped monthly VND quotes with VAT as returned,
// without ordering resources or locking prices. It validates the setup before
// authentication and shares one request budget and ten-minute child deadline.
// Quantities project independent units; CostComplete is always false. By default,
// an error returns nil output. AllowPartial retains surviving results with a
// DiscoveryError, but invalid input always rejects the whole setup. See
// docs/design/cost-estimate.md for coverage, limits, and partial-result semantics.
func (c *Client) Estimate(ctx context.Context, in *EstimateInput) (*EstimateOutput, error) {
	if err := validateEstimate(in, c.c.Region()); err != nil {
		return nil, err
	}
	s, err := discovery.New(ctx, in.MaxRequests)
	if err != nil {
		return nil, estimateInvalid("MaxRequests")
	}
	defer s.Close()
	d := &estimateRun{client: c.c, session: s, input: in, catalogs: map[string]estimateCatalogResult{}, output: &EstimateOutput{
		Items: []EstimateItem{}, Coverage: []EstimateCoverage{},
		Total:    EstimateTotal{Currency: "VND", Tax: "as-returned", Basis: "sum-of-independent-monthly-quotes", Scope: "specified-recurring-components"},
		Metadata: DiscoveryMetadata{Region: c.c.Region(), Complete: true, Failures: []DiscoveryFailure{}, Notes: estimateNotes()},
	}}
	d.initialize()
	if err = s.AddRows(len(d.output.Items)); err != nil {
		return nil, estimateLimit(err)
	}
	if err = d.prepare(); err != nil {
		return nil, estimateLimit(err)
	}
	if !d.stopped {
		if err = s.Preflight(d.distinctQuotes()); err != nil {
			if !in.AllowPartial {
				return nil, estimateLimit(err)
			}
			d.record("", err)
			d.stopped = true
		}
	}
	if !d.stopped {
		if err = d.quote(); err != nil {
			return nil, estimateLimit(err)
		}
	}
	return d.finish()
}

func (d *estimateRun) initialize() {
	add := func(key, kind, server, region, zone string, quantity int) {
		d.output.Items = append(d.output.Items, EstimateItem{Key: key, Kind: kind, ServerKey: server, Region: region, ZoneID: zone, Quantity: quantity, Price: DiscoveryPrice{Status: "failed", Source: estimateOperation}, Breakdown: []EstimateCharge{}})
		d.requests = append(d.requests, nil)
	}
	for _, s := range d.input.Servers {
		add(s.Key, "server", "", d.client.Region(), s.ZoneID, s.Quantity)
	}
	for _, v := range d.input.Volumes {
		add(v.Key, "volume", v.ServerKey, d.client.Region(), v.ZoneID, v.Quantity)
	}
	for _, ip := range d.input.PublicIPs {
		add(ip.Key, ip.Kind, "", d.client.Region(), "", ip.Quantity)
	}
	for _, s := range d.input.Storage {
		add(s.Key, "object-storage", "", "", "", s.Quantity)
	}
	d.addCoverage()
}

func (d *estimateRun) record(key string, err error) {
	err = estimateLimit(err)
	d.errors = append(d.errors, err)
	d.output.Metadata.Complete = false
	failure := DiscoveryFailure{Operation: estimateOperation, ItemKey: key, Code: "DiscoveryIncomplete"}
	var api *core.APIError
	if errors.As(err, &api) {
		failure.Status = api.StatusCode
		failure.Code = estimateFailureCode(api)
	}
	var arithmetic *EstimateArithmeticError
	reason := "request-failed"
	if errors.As(err, &arithmetic) {
		if arithmetic.Stage == "multiplication" {
			failure.Code = "EstimateMultiplicationOverflow"
		} else {
			failure.Code = "EstimateSubtotalOverflow"
		}
		reason = "arithmetic-overflow"
	}
	d.output.Metadata.Failures = append(d.output.Metadata.Failures, failure)
	scope := key
	if scope == "" {
		scope = "setup"
	}
	d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: "processing", Scope: scope, Status: "failed", Reason: reason})
}

func estimateLimit(err error) error {
	if errors.Is(err, discovery.ErrLimit) && !errors.Is(err, ErrEstimateLimit) {
		return fmt.Errorf("%w: %w", ErrEstimateLimit, err)
	}
	var api *core.APIError
	if errors.As(err, &api) {
		safe := *api
		safe.Message = "estimate request failed; body withheld"
		safe.Code = estimateFailureCode(api)
		return &safe
	}
	return err
}

func (d *estimateRun) terminal(err error) bool {
	var api *core.APIError
	return d.client.DiscoveryFatal(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, discovery.ErrLimit) || (errors.As(err, &api) && (api.StatusCode == 401 || api.StatusCode == 429))
}

func (d *estimateRun) prepare() error {
	for i := range d.output.Items {
		if d.stopped {
			break
		}
		req, err := d.prepareItem(i)
		if err == nil {
			d.requests[i] = req
			continue
		}
		if errors.Is(err, core.ErrInvalidInput) || !d.input.AllowPartial {
			return err
		}
		d.record(d.output.Items[i].Key, err)
		if d.terminal(err) {
			d.stopped = true
		}
	}
	return nil
}
