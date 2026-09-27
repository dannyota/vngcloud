package volume

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// CreateVolumeInput creates a volume. QuoteCreateVolume takes the same
// Input and prices it, per the SDK's paid-write convention that a quote is
// built from the create's own code.
type CreateVolumeInput struct {
	// Name must not match an existing volume's name exactly.
	Name         string `vngcloud:"required"`
	ZoneID       string `vngcloud:"required"`
	Size         int    `vngcloud:"required"`
	VolumeTypeID string `vngcloud:"required"`

	// AutoRenew is sent as isEnableAutoRenew; false by default, so nothing
	// renews from credit without a command.
	AutoRenew bool

	// MaxPrice is VND a month; a paid CreateVolume refuses to order above
	// it. QuoteCreateVolume ignores it.
	MaxPrice float64
	// NoWait skips CreateVolume's post-create wait. QuoteCreateVolume
	// ignores it.
	NoWait bool
}

// createVolumeBody is CreateVolume and QuoteCreateVolume's shared request
// body, built by buildCreateVolumeBody.
type createVolumeBody struct {
	Name              string `json:"name"`
	Size              int    `json:"size"`
	VolumeTypeID      string `json:"volumeTypeId"`
	ZoneID            string `json:"zoneId"`
	IsEnableAutoRenew bool   `json:"isEnableAutoRenew"`
}

// buildCreateVolumeBody validates in and builds the body CreateVolume and
// QuoteCreateVolume both send.
func buildCreateVolumeBody(op string, in *CreateVolumeInput) (createVolumeBody, error) {
	if err := core.CheckRequired(op, in); err != nil {
		return createVolumeBody{}, err
	}
	if err := core.CheckPathID(op, "VolumeTypeID", in.VolumeTypeID); err != nil {
		return createVolumeBody{}, err
	}
	return createVolumeBody{
		Name:              in.Name,
		Size:              in.Size,
		VolumeTypeID:      in.VolumeTypeID,
		ZoneID:            in.ZoneID,
		IsEnableAutoRenew: in.AutoRenew,
	}, nil
}

// QuoteCreateVolume prices the volume Input would create, without ordering
// it. It builds the same body a create sends and quotes it with
// ActionCreate. It ignores Input.MaxPrice and Input.NoWait, which govern
// only an actual create.
func (c *Client) QuoteCreateVolume(ctx context.Context, in *CreateVolumeInput) (*pricing.GetQuoteOutput, error) {
	const op = "volume.QuoteCreateVolume"
	body, err := buildCreateVolumeBody(op, in)
	if err != nil {
		return nil, err
	}
	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceVolume,
		Action:       pricing.ActionCreate,
		ResourceInfo: info,
	})
}

// CreateVolumeOutput is CreateVolume's result. Volume is filled from
// CreateVolume's own post-create wait unless NoWait is set, in which case it
// holds only the UUID and Name the caller gave, since the create response's
// other fields are not confirmed live. MonthlyPrice is the quote's own
// OptimumPrice, the price actually checked against Input.MaxPrice, which can
// differ from what the order itself later bills.
type CreateVolumeOutput struct {
	Volume       Volume
	MonthlyPrice float64
}

// createVolumeResponse decodes CreateVolume's response. Its only confirmed
// field is data.uuid; every other field is unverified, so this never
// decodes into the bare Volume model.
type createVolumeResponse struct {
	Data struct {
		UUID string `json:"uuid"`
	} `json:"data"`
}

// CreateVolume orders a volume. Before any request, it rejects a NaN,
// +Inf, -Inf, or negative Input.MaxPrice with core.ErrInvalidInput: the
// price guard below (quote.OptimumPrice > Input.MaxPrice) cannot compare
// any of those safely. It then lists volumes by Input.Name and refuses,
// also with core.ErrInvalidInput and before any pricing or order request,
// when one already exists with that exact name, since a rerun after an
// unclear failure must never risk ordering a second volume under the same
// name.
//
// CreateVolume builds one order body from Input with buildCreateVolumeBody
// (ADR 0002 rule 8) and sends that same body to QuoteCreateVolume's own
// quote endpoint first. It refuses with vngcloud.ErrPriceAboveMax, ordering
// nothing, when the quote's OptimumPrice exceeds Input.MaxPrice (default
// 0). A quote response missing optimumPrice is itself an error from
// pricing.GetQuote, never a silent price of 0.
//
// The order is a POST and is never retried after a failure that may have
// already reached the server: after any error that is not a 4xx
// *core.APIError, the volume may exist, and the caller lists volumes by
// Name and matches it exactly before ordering again.
//
// Without NoWait, CreateVolume waits up to 5 minutes, polling GetVolume
// every 2 seconds, for the new volume to reach AVAILABLE. A 404 during that
// wait keeps polling, since a volume just created may not be readable at
// once. If the volume reaches ERROR instead, or the bound runs out, or a
// read or a sleep in that wait fails, such as from a canceled ctx, the
// returned error wraps ErrFailed or ErrNotSettled and Output.Volume still
// holds the last volume a read returned, falling back to one with only the
// new UUID and Input.Name if no read ever succeeded. NoWait skips that wait
// and returns at once with Output.Volume holding only the new UUID and
// Input.Name.
func (c *Client) CreateVolume(ctx context.Context, in *CreateVolumeInput) (*CreateVolumeOutput, error) {
	const op = "volume.CreateVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckMaxPrice(op, in.MaxPrice); err != nil {
		return nil, err
	}
	body, err := buildCreateVolumeBody(op, in)
	if err != nil {
		return nil, err
	}
	if err := c.refuseIfVolumeNameExists(ctx, op, in.Name); err != nil {
		return nil, err
	}

	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	quote, err := c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceVolume,
		Action:       pricing.ActionCreate,
		ResourceInfo: info,
	})
	if err != nil {
		return nil, err
	}
	if err := core.CheckPriceAboveMax(op, quote.OptimumPrice, in.MaxPrice); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp createVolumeResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.volumeURL("v2", []string{projectID, "volumes"}, nil),
		Body:      body,
		OK:        []int{202},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousVolumeCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the volume may exist, list volumes and match the name exactly before creating it again"}
	}

	fallback := Volume{UUID: resp.Data.UUID, Name: in.Name}
	if in.NoWait {
		return &CreateVolumeOutput{Volume: fallback, MonthlyPrice: quote.OptimumPrice}, nil
	}
	settled, waitErr := c.waitVolumeAvailable(ctx, op, resp.Data.UUID)
	if settled == nil {
		settled = &fallback
	}
	return &CreateVolumeOutput{Volume: *settled, MonthlyPrice: quote.OptimumPrice}, waitErr
}

// wrapAmbiguousVolumeCreateErr wraps err, from the create POST just sent,
// with a hint to list volumes before creating again, unless err is already a
// 4xx *core.APIError: a 4xx means the server rejected the request outright,
// so nothing was created and the exact same call is safe to retry. Any
// other error leaves whether the volume was created unknown.
func wrapAmbiguousVolumeCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list volumes and match the name exactly before creating it again: %w", op, err)
}

// refuseIfVolumeNameExists refuses to order a volume named name when one
// already exists on the account, before any pricing or order request.
func (c *Client) refuseIfVolumeNameExists(ctx context.Context, op, name string) error {
	existing, err := c.findVolumeByName(ctx, name)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("%w: %s: a volume named %q already exists", core.ErrInvalidInput, op, name)
	}
	return nil
}

// findVolumeByName returns the volume named exactly name from one
// ListVolumes read, or nil if none matches. It scans every returned item for
// an exact Name match rather than trusting the list's own name filter to
// match exactly, since that filter's matching behavior (exact vs.
// substring) is unconfirmed.
func (c *Client) findVolumeByName(ctx context.Context, name string) (*Volume, error) {
	out, err := c.ListVolumes(ctx, &ListVolumesInput{Name: name})
	if err != nil {
		return nil, err
	}
	for i := range out.Items {
		if out.Items[i].Name == name {
			return &out.Items[i], nil
		}
	}
	return nil, nil
}

// waitVolumeAvailable is CreateVolume's post-create wait unless NoWait is
// set: it reads volumeID with GetVolume until its Status reaches AVAILABLE
// or ERROR; any other status, including a 404 (a volume just created may
// not be readable at once), keeps it polling.
func (c *Client) waitVolumeAvailable(ctx context.Context, op, volumeID string) (*Volume, error) {
	var vol *Volume
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: volumeID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			vol = &out.Volume
			switch {
			case vol.IsAvailable():
				return true, nil
			case isVolumeError(vol.Status):
				return true, fmt.Errorf("%w: %s: volume %s is ERROR", ErrFailed, op, volumeID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: volume %s did not reach AVAILABLE within %s; the volume exists and this create must not be repeated",
				ErrNotSettled, op, volumeID, volumeWaitBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: volume %s: %w", ErrNotSettled, op, volumeID, err)
	}
	return vol, err
}
