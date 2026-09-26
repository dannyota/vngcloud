package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ErrPriceAboveMax means CreateLogProject's quote priced the order above
// Input.MaxPrice. No order was sent.
var ErrPriceAboveMax = errors.New("monitor: log project price above MaxPrice")

// logProjectPollInterval, logProjectCreateWaitBound, and
// logProjectDeleteWaitBound are CreateLogProject and DeleteLogProject's own
// wait cadence and bounds, per the design's wait table.
const (
	logProjectPollInterval    = 2 * time.Second
	logProjectCreateWaitBound = 120 * time.Second
	logProjectDeleteWaitBound = 60 * time.Second
)

// CreateLogProjectOutput is CreateLogProject's result. LogProject is filled
// only once CreateLogProject's own post-order wait finds the ordered
// project by name; NoWait skips that wait, so LogProject stays at its zero
// value. OrderID is the order response's own orderId and is set either way,
// but is not guaranteed non-empty: a live free order returned it empty or
// null.
type CreateLogProjectOutput struct {
	LogProject LogProject
	OrderID    string
}

// logProjectOrderResponse is the order POST's own response shape: a live
// order confirmed exactly amount, orderId, and paymentUrl, none of
// LogProject's own fields. amount and paymentUrl are not modeled, since
// CreateLogProject has no use for them. A live free order returned orderId
// empty or null; OrderID routes through flexibleString so a numeric orderId,
// if the API ever sends one, still decodes rather than failing the whole
// response.
type logProjectOrderResponse struct {
	OrderID string `json:"orderId"`
}

// UnmarshalJSON decodes logProjectOrderResponse with OrderID routed through
// flexibleString.
func (r *logProjectOrderResponse) UnmarshalJSON(data []byte) error {
	var aux struct {
		OrderID flexibleString `json:"orderId"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.OrderID = string(aux.OrderID)
	return nil
}

// CreateLogProject orders a log project. It quotes first with
// QuoteCreateLogProject and refuses with ErrPriceAboveMax, ordering
// nothing, when the quote's OptimumPrice exceeds Input.MaxPrice (default
// 0): CreateLogProjectInput{Name: "app"} therefore only ever orders a
// project whose class and retention price at 0 VND. The order itself is
// built fresh from a second, independent ListLogProjectClasses read and
// buildLogProjectOrderBody call (ADR 0002 rule 8), the same as the quote
// above, since the price can change between the two requests; neither step
// caches the class list.
//
// The order is a POST and is never retried after a failure that may have
// already reached the server: after any error that is not a 4xx
// *core.APIError or core.ErrInvalidInput, the project may have been
// ordered, and the caller lists projects by Name before ordering again.
//
// The order response is confirmed live to carry only amount, orderId, and
// paymentUrl: it names no project id, name, or status (see LogProject's
// doc comment). A live free order returned orderId empty or null, so
// Output.OrderID is not guaranteed non-empty either way. Without NoWait,
// CreateLogProject therefore waits up to 120 seconds for a project named
// Input.Name to appear, by ListLogProjects, at LogProjectStatusActive,
// looking it up by the name the order itself just sent rather than by
// anything the order response might carry. If the bound runs out, or a
// read or a sleep in that wait fails, such as from a canceled ctx, the
// returned error wraps dns.ErrNotSettled, reusing vDNS's own sentinel per
// the design: the write must not be repeated. NoWait skips that wait and
// returns at once, with Output.LogProject at its zero value and only
// Output.OrderID set, from the order response's own orderId.
func (c *Client) CreateLogProject(ctx context.Context, in *CreateLogProjectInput) (*CreateLogProjectOutput, error) {
	const op = "monitor.CreateLogProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	quote, err := c.QuoteCreateLogProject(ctx, in)
	if err != nil {
		return nil, err
	}
	if quote.OptimumPrice > in.MaxPrice {
		return nil, fmt.Errorf("%w: %s: quote %.0f VND exceeds MaxPrice %.0f VND", ErrPriceAboveMax, op, quote.OptimumPrice, in.MaxPrice)
	}

	classes, err := c.ListLogProjectClasses(ctx, nil)
	if err != nil {
		return nil, err
	}
	body, err := buildLogProjectOrderBody(op, in, classes.Items)
	if err != nil {
		return nil, err
	}

	var resp logProjectOrderResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.logBillingRoute([]string{"log", "quotas"}),
		Body:      body,
		OK:        []int{200, 201},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousLogProjectOrderErr(op, err)
	}

	if in.NoWait {
		return &CreateLogProjectOutput{OrderID: resp.OrderID}, nil
	}

	found, waitErr := c.waitLogProjectActive(ctx, op, in.Name)
	if found == nil {
		// The wait never found the project by name, from a timeout or a
		// read failure; the order response carries no project fields to
		// fall back to, so LogProject stays at its zero value.
		return &CreateLogProjectOutput{OrderID: resp.OrderID}, waitErr
	}
	return &CreateLogProjectOutput{LogProject: *found, OrderID: resp.OrderID}, waitErr
}

// wrapAmbiguousLogProjectOrderErr wraps err, from the order POST just sent,
// with a hint to list projects before ordering again, unless err is
// already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so nothing was ordered and the exact same call is safe
// to retry. Any other error, a 5xx or a failure before any response ever
// came back, leaves whether the project was ordered unknown.
func wrapAmbiguousLogProjectOrderErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: order may have already reached the server; list log projects by name before ordering again: %w", op, err)
}

// findLogProjectByName returns the log project named exactly name from one
// ListLogProjects read, or nil if none matches. It lists at
// logProjectDefaultPageSize and scans every returned item for an exact
// ProjectName match, rather than trusting the list's own query filter to
// return only exact matches: that filter's matching behavior (exact vs.
// substring) is unconfirmed.
func (c *Client) findLogProjectByName(ctx context.Context, op, name string) (*LogProject, error) {
	out, err := c.listLogProjects(ctx, op, &ListLogProjectsInput{Query: name, Size: logProjectDefaultPageSize})
	if err != nil {
		return nil, err
	}
	for i := range out.Items {
		if out.Items[i].ProjectName == name {
			return &out.Items[i], nil
		}
	}
	return nil, nil
}

// waitLogProjectActive is CreateLogProject's post-order wait unless NoWait
// is set: it lists log projects by name until an exact match's Status is
// LogProjectStatusActive, or logProjectCreateWaitBound elapses first.
func (c *Client) waitLogProjectActive(ctx context.Context, op, name string) (*LogProject, error) {
	var found *LogProject
	err := poll(ctx, c.now, c.sleep, logProjectPollInterval, logProjectCreateWaitBound,
		func(ctx context.Context) (bool, error) {
			project, err := c.findLogProjectByName(ctx, op, name)
			if err != nil {
				return true, err
			}
			if project == nil {
				return false, nil
			}
			found = project
			return project.Status == LogProjectStatusActive, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: log project %q was ordered; do not order it again", dns.ErrNotSettled, op, name)
		},
	)
	if err != nil && !errors.Is(err, dns.ErrNotSettled) {
		err = fmt.Errorf("%w: %s: log project %q: %w", dns.ErrNotSettled, op, name, err)
	}
	return found, err
}

// DeleteLogProjectInput identifies the log project to delete, and whether
// to also purge it from trash in the same call.
type DeleteLogProjectInput struct {
	LogProjectID string `vngcloud:"required"`

	Purge  bool
	NoWait bool
}

type DeleteLogProjectOutput struct{}

// DeleteLogProject moves a log project to trash, stopping its billing; its
// logs are lost. With Purge, it then also deletes the project from trash,
// as a second request in the same call, so a purge is never sent without
// the delete that logically precedes it. If that first, trash-moving
// delete itself 404s, DeleteLogProject still sends the purge when Purge is
// set, since the most likely explanation is that the project already sits
// in trash from an earlier call, and Purge's own job is to make sure the
// project ends up gone either way; without Purge, that same 404 is
// returned to the caller unchanged, the same not-found result any other
// delete in this SDK returns for an already-gone resource.
//
// Both the delete and the purge are DELETE requests and keep the
// transport's normal retries.
//
// Without NoWait, DeleteLogProject first reads the project to record its
// Status and BillingStatus as a baseline, then, after the delete (and the
// purge, when requested) succeeds, waits up to 60 seconds for a read of the
// project to either 404 or no longer match that baseline: the design's own
// settle condition, "Get is 404, or the project is in trash," covers both
// ways an unconfirmed response might show the change. When Purge is set
// and that baseline read itself 404s, the project is already gone from the
// live list (seen live for a free project, gone from trash within about a
// second of an earlier delete): DeleteLogProject still sends the delete
// and the purge, tolerating a 404 from either, and returns success at
// once, with no wait, since there is no baseline left to wait against.
// Without Purge, that same baseline 404 is returned unchanged, the
// ordinary not-found result any other delete in this SDK returns for an
// already-gone resource. If the bound runs out, or a read or a sleep in
// the wait fails, such as from a canceled ctx, the returned error wraps
// dns.ErrNotSettled: the write must not be repeated. NoWait skips the
// baseline read and the wait, and returns as soon as the delete (and the
// purge, when requested) succeeds.
func (c *Client) DeleteLogProject(ctx context.Context, in *DeleteLogProjectInput) (*DeleteLogProjectOutput, error) {
	const op = "monitor.DeleteLogProject"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LogProjectID", in.LogProjectID); err != nil {
		return nil, err
	}

	var before *LogProject
	baselineGone := false
	if !in.NoWait {
		b, err := c.getLogProject(ctx, op, in.LogProjectID)
		switch {
		case err == nil:
			before = b
		case in.Purge && core.IsNotFound(err):
			baselineGone = true
		default:
			return nil, err
		}
	}

	deleteErr := c.deleteLogProjectRequest(ctx, op, []string{"log", "quotas", in.LogProjectID})
	if deleteErr != nil && (!in.Purge || !core.IsNotFound(deleteErr)) {
		return nil, deleteErr
	}

	if in.Purge {
		if err := c.deleteLogProjectRequest(ctx, op, []string{"trash", "log", "quotas", in.LogProjectID}); err != nil && !core.IsNotFound(err) {
			return nil, err
		}
	}

	if in.NoWait || baselineGone {
		return &DeleteLogProjectOutput{}, nil
	}
	if err := c.waitLogProjectTrashed(ctx, op, in.LogProjectID, before); err != nil {
		return &DeleteLogProjectOutput{}, err
	}
	return &DeleteLogProjectOutput{}, nil
}

// deleteLogProjectRequest sends one DELETE under the billing-api v1
// prefix, for either the trash-moving delete (parts under "log", "quotas")
// or the purge (parts under "trash", "log", "quotas") DeleteLogProject
// sends. Both responses' shape and status are unconfirmed, so this
// discards the body and accepts either 200 or 204.
func (c *Client) deleteLogProjectRequest(ctx context.Context, op string, parts []string) error {
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.logBillingRouteV1(parts),
		OK:        []int{200, 204},
	}
	return c.c.DoJSON(ctx, req, nil)
}

// waitLogProjectTrashed is DeleteLogProject's post-write wait unless NoWait
// is set. before is the project as read just before the delete (nil only
// when NoWait already skipped that read, in which case this is never
// called); the wait settles the moment a read either 404s or no longer
// matches before's Status and BillingStatus, whichever comes first.
func (c *Client) waitLogProjectTrashed(ctx context.Context, op, id string, before *LogProject) error {
	err := poll(ctx, c.now, c.sleep, logProjectPollInterval, logProjectDeleteWaitBound,
		func(ctx context.Context) (bool, error) {
			project, err := c.getLogProject(ctx, op, id)
			if err != nil {
				if core.IsNotFound(err) {
					return true, nil
				}
				return true, err
			}
			if project.Status != before.Status || project.BillingStatus != before.BillingStatus {
				return true, nil
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: log project %s was accepted; do not delete or purge it again", dns.ErrNotSettled, op, id)
		},
	)
	if err != nil && !errors.Is(err, dns.ErrNotSettled) {
		err = fmt.Errorf("%w: %s: log project %s: %w", dns.ErrNotSettled, op, id, err)
	}
	return err
}
