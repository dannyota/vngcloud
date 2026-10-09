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

// ResizeVolumeInput grows a volume. Size must be greater than the volume's
// current size; a shrink or an equal size is refused, since shrinking would
// cut off the end of the data.
type ResizeVolumeInput struct {
	VolumeID string `vngcloud:"required"`
	Size     int    `vngcloud:"required"`

	// MaxPrice is VND a month; a paid ResizeVolume refuses to send above
	// it. QuoteResizeVolume ignores it.
	MaxPrice float64
	// NoWait skips ResizeVolume's post-resize wait. QuoteResizeVolume
	// ignores it.
	NoWait bool
}

// resizeVolumeBody is ResizeVolume and QuoteResizeVolume's shared request
// body: NewVolumeTypeID is always the volume's current VolumeTypeID, which
// the API requires on every resize, so a type never changes by accident.
type resizeVolumeBody struct {
	NewSize         int    `json:"newSize"`
	NewVolumeTypeID string `json:"newVolumeTypeId"`
}

// buildResizeVolumeBody validates in against current, the volume's own
// pre-resize state, and builds the body ResizeVolume and QuoteResizeVolume
// both send. A Size at or below current.Size, or Size 0 or less, is
// core.ErrInvalidInput.
func buildResizeVolumeBody(op string, in *ResizeVolumeInput, current Volume) (resizeVolumeBody, error) {
	if err := core.CheckRequired(op, in); err != nil {
		return resizeVolumeBody{}, err
	}
	if err := core.CheckPathID(op, "VolumeID", in.VolumeID); err != nil {
		return resizeVolumeBody{}, err
	}
	if in.Size <= 0 || uint64(in.Size) <= current.Size {
		return resizeVolumeBody{}, fmt.Errorf("%w: %s: Size %d must be greater than the current size %d", core.ErrInvalidInput, op, in.Size, current.Size)
	}
	return resizeVolumeBody{NewSize: in.Size, NewVolumeTypeID: current.VolumeTypeID}, nil
}

// QuoteResizeVolume prices the grow Input describes, without sending it. It
// reads the volume fresh, on every call, to learn its current size and
// type, then builds the same body ResizeVolume sends and quotes it with
// ActionResize, adding volumeId to the quote's own resourceInfo (the
// design's resize quote request table lists it, but the resize PUT itself
// never sends it, since the PUT already names the volume in its path). It
// ignores Input.MaxPrice and Input.NoWait, which govern only an actual
// resize.
func (c *Client) QuoteResizeVolume(ctx context.Context, in *ResizeVolumeInput) (*pricing.GetQuoteOutput, error) {
	const op = "volume.QuoteResizeVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VolumeID", in.VolumeID); err != nil {
		return nil, err
	}
	current, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: in.VolumeID})
	if err != nil {
		return nil, err
	}
	body, err := buildResizeVolumeBody(op, in, current.Volume)
	if err != nil {
		return nil, err
	}
	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	info["volumeId"] = in.VolumeID
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceVolume,
		Action:       pricing.ActionResize,
		ResourceInfo: info,
	})
}

// ResizeVolumeOutput is ResizeVolume's result. MonthlyPrice is the quote's
// own OptimumPrice, the price actually checked against Input.MaxPrice.
type ResizeVolumeOutput struct {
	Volume       Volume
	MonthlyPrice float64
}

// ResizeVolume grows a volume. It reads the volume first: Input.Size at or
// below the current size is core.ErrInvalidInput, sending nothing, since a
// shrink would cut off the end of the data, and a Status other than
// AVAILABLE or IN-USE is ErrUnexpectedStatus, sending nothing, since any
// other status means the volume is already changing.
//
// Before any request, ResizeVolume also rejects a NaN, +Inf, -Inf, or
// negative Input.MaxPrice with core.ErrInvalidInput. It then builds one
// request body with buildResizeVolumeBody (ADR 0002 rule 8), the same
// builder QuoteResizeVolume uses, sending the volume's current
// VolumeTypeID back as newVolumeTypeId so a type never changes by
// accident. It sends that body to the quote endpoint first and refuses
// with vngcloud.ErrPriceAboveMax, sending nothing, when the quote's
// OptimumPrice exceeds Input.MaxPrice (default 0).
//
// The resize PUT is sent at most once (transport.Request.Once), per ADR
// 0003: a resend would act on the size read this call already took, which
// only grows staler. A 4xx response proves the server never acted and is
// returned as is; any other failure returns an error wrapping
// ErrNotSettled naming GetVolume as the read that confirms what actually
// happened, since ResizeVolume itself, a paid write, must never be the
// suggested recovery for an ambiguous failure.
//
// Without NoWait, ResizeVolume then waits up to 5 minutes, polling
// GetVolume every 2 seconds, for a read showing the new size with Status
// AVAILABLE or IN-USE. ERROR wraps ErrFailed; the bound running out, or a
// read or a sleep failing, wraps ErrNotSettled, again naming GetVolume as
// the read to run. NoWait returns at once instead, with Output.Volume
// holding the pre-resize read. The filesystem inside a server that has this
// volume attached must still be grown separately; ResizeVolume only grows
// the block device.
func (c *Client) ResizeVolume(ctx context.Context, in *ResizeVolumeInput) (*ResizeVolumeOutput, error) {
	const op = "volume.ResizeVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckMaxPrice(op, in.MaxPrice); err != nil {
		return nil, err
	}
	current, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: in.VolumeID})
	if err != nil {
		return nil, err
	}
	if !current.Volume.IsAvailable() && !current.Volume.IsInUse() {
		return nil, fmt.Errorf("%w: %s: volume %s is %q", ErrUnexpectedStatus, op, in.VolumeID, current.Volume.Status)
	}
	body, err := buildResizeVolumeBody(op, in, current.Volume)
	if err != nil {
		return nil, err
	}

	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	info["volumeId"] = in.VolumeID
	quote, err := c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceVolume,
		Action:       pricing.ActionResize,
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
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID, "resize"}, nil),
		Body:      body,
		OK:        []int{202},
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		if is4xxAPIError(err) {
			return nil, err
		}
		return &ResizeVolumeOutput{Volume: current.Volume}, fmt.Errorf("%w: %s: volume %s: call GetVolume to check its status: %w", ErrNotSettled, op, in.VolumeID, err)
	}

	if in.NoWait {
		return &ResizeVolumeOutput{Volume: current.Volume, MonthlyPrice: quote.OptimumPrice}, nil
	}
	settled, waitErr := c.waitVolumeResized(ctx, op, in.VolumeID, in.Size)
	if settled == nil {
		settled = &current.Volume
	}
	return &ResizeVolumeOutput{Volume: *settled, MonthlyPrice: quote.OptimumPrice}, waitErr
}

// waitVolumeResized is ResizeVolume's post-resize wait unless NoWait is
// set: it reads volumeID with GetVolume until its Size reaches wantSize
// with Status AVAILABLE or IN-USE (settled), or ERROR (failed); any other
// outcome keeps it polling.
func (c *Client) waitVolumeResized(ctx context.Context, op, volumeID string, wantSize int) (*Volume, error) {
	var vol *Volume
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: volumeID})
			if err != nil {
				return true, err
			}
			vol = &out.Volume
			switch {
			case isVolumeError(vol.Status):
				return true, fmt.Errorf("%w: %s: volume %s is ERROR", ErrFailed, op, volumeID)
			case vol.Size == uint64(wantSize) && (vol.IsAvailable() || vol.IsInUse()): //nolint:gosec // wantSize is Input.Size, already checked positive by buildResizeVolumeBody before this wait runs
				return true, nil
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: volume %s did not confirm resize to %d GB within %s; call GetVolume to check its status",
				ErrNotSettled, op, volumeID, wantSize, volumeWaitBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: volume %s: %w", ErrNotSettled, op, volumeID, err)
	}
	return vol, err
}
