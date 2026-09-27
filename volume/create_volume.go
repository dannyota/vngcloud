package volume

import (
	"context"

	"danny.vn/vngcloud/internal/core"
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
