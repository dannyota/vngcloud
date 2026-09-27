package compute

import (
	"context"
	"encoding/base64"
	"fmt"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/pricing"
)

// CreateServerInput creates a server. QuoteCreateServer takes the same
// Input and prices it, per the SDK's paid-write convention that a quote is
// built from the create's own code. Every ID below, in a path or a body, is
// checked with core.CheckPathID before any request, including the quote.
type CreateServerInput struct {
	// Name must not match an existing server's name exactly.
	Name     string `vngcloud:"required"`
	ZoneID   string `vngcloud:"required"`
	FlavorID string `vngcloud:"required"`
	ImageID  string `vngcloud:"required"`
	// VPCID is sent as networkId.
	VPCID    string `vngcloud:"required"`
	SubnetID string `vngcloud:"required"`
	// SecurityGroupIDs must hold at least one ID; the SDK picks no default.
	SecurityGroupIDs []string `vngcloud:"required"`
	// SSHKeyID is the only login the SDK sets up.
	SSHKeyID       string `vngcloud:"required"`
	RootDiskSize   int    `vngcloud:"required"`
	RootDiskTypeID string `vngcloud:"required"`

	// DataDiskSize and DataDiskTypeID together add one optional data disk;
	// set both or neither.
	DataDiskSize   int
	DataDiskTypeID string
	DataDiskName   string

	ServerGroupID string

	// UserData is cloud-init text. The SDK base64-encodes it and sends
	// userDataBase64Encoded true. It is never sent to QuoteCreateServer's
	// quote.
	UserData string

	// AutoRenew is sent as isEnableAutoRenew; false by default, so nothing
	// renews from credit without a command.
	AutoRenew bool

	// MaxPrice is VND a month; a paid CreateServer refuses to order above
	// it. QuoteCreateServer ignores it.
	MaxPrice float64
	// NoWait skips CreateServer's post-create wait. QuoteCreateServer
	// ignores it.
	NoWait bool
}

// createServerBody is CreateServer and QuoteCreateServer's shared request
// body, built by buildCreateServerBody. EncryptionVolume and
// IsEnableAutoRenew carry no omitempty tag, so a false value still reaches
// the wire: the server requires encryptionVolume, and an omitted
// isEnableAutoRenew must never be read as true.
type createServerBody struct {
	Name                  string   `json:"name"`
	ZoneID                string   `json:"zoneId"`
	FlavorID              string   `json:"flavorId"`
	ImageID               string   `json:"imageId"`
	NetworkID             string   `json:"networkId"`
	SubnetID              string   `json:"subnetId"`
	SecurityGroup         []string `json:"securityGroup"`
	SSHKeyID              string   `json:"sshKeyId"`
	RootDiskSize          int      `json:"rootDiskSize"`
	RootDiskTypeID        string   `json:"rootDiskTypeId"`
	EncryptionVolume      bool     `json:"encryptionVolume"`
	DataDiskName          string   `json:"dataDiskName,omitempty"`
	DataDiskSize          int      `json:"dataDiskSize,omitempty"`
	DataDiskTypeID        string   `json:"dataDiskTypeId,omitempty"`
	ServerGroupID         string   `json:"serverGroupId,omitempty"`
	UserData              string   `json:"userData,omitempty"`
	UserDataBase64Encoded bool     `json:"userDataBase64Encoded,omitempty"`
	IsEnableAutoRenew     bool     `json:"isEnableAutoRenew"`
}

// buildCreateServerBody validates in and builds the body CreateServer and
// QuoteCreateServer both send. It never sets attachFloating, userName,
// userPassword, osLicence, or expirePassword: this SDK has no field for a
// public IP or password login on create.
func buildCreateServerBody(op string, in *CreateServerInput) (createServerBody, error) {
	if err := core.CheckRequired(op, in); err != nil {
		return createServerBody{}, err
	}
	if len(in.SecurityGroupIDs) == 0 {
		return createServerBody{}, fmt.Errorf("%w: %s requires at least one SecurityGroupIDs entry", core.ErrInvalidInput, op)
	}
	if (in.DataDiskSize > 0) != (in.DataDiskTypeID != "") {
		return createServerBody{}, fmt.Errorf("%w: %s requires DataDiskSize and DataDiskTypeID together or neither", core.ErrInvalidInput, op)
	}
	for _, id := range [...]struct{ field, value string }{
		{"FlavorID", in.FlavorID},
		{"ImageID", in.ImageID},
		{"VPCID", in.VPCID},
		{"SubnetID", in.SubnetID},
		{"SSHKeyID", in.SSHKeyID},
		{"RootDiskTypeID", in.RootDiskTypeID},
	} {
		if err := core.CheckPathID(op, id.field, id.value); err != nil {
			return createServerBody{}, err
		}
	}
	for i, sgID := range in.SecurityGroupIDs {
		if err := core.CheckPathID(op, fmt.Sprintf("SecurityGroupIDs[%d]", i), sgID); err != nil {
			return createServerBody{}, err
		}
	}
	if in.ServerGroupID != "" {
		if err := core.CheckPathID(op, "ServerGroupID", in.ServerGroupID); err != nil {
			return createServerBody{}, err
		}
	}
	if in.DataDiskTypeID != "" {
		if err := core.CheckPathID(op, "DataDiskTypeID", in.DataDiskTypeID); err != nil {
			return createServerBody{}, err
		}
	}

	body := createServerBody{
		Name:              in.Name,
		ZoneID:            in.ZoneID,
		FlavorID:          in.FlavorID,
		ImageID:           in.ImageID,
		NetworkID:         in.VPCID,
		SubnetID:          in.SubnetID,
		SecurityGroup:     in.SecurityGroupIDs,
		SSHKeyID:          in.SSHKeyID,
		RootDiskSize:      in.RootDiskSize,
		RootDiskTypeID:    in.RootDiskTypeID,
		EncryptionVolume:  false,
		DataDiskName:      in.DataDiskName,
		DataDiskSize:      in.DataDiskSize,
		DataDiskTypeID:    in.DataDiskTypeID,
		ServerGroupID:     in.ServerGroupID,
		IsEnableAutoRenew: in.AutoRenew,
	}
	if in.UserData != "" {
		body.UserData = base64.StdEncoding.EncodeToString([]byte(in.UserData))
		body.UserDataBase64Encoded = true
	}
	return body, nil
}

// QuoteCreateServer prices the server Input would create, without ordering
// it. It builds the same body a create sends, with UserData and
// UserDataBase64Encoded cleared first, since user data is not priced and
// must never reach the billing gateway, and quotes it with ActionCreate.
// It ignores Input.MaxPrice and Input.NoWait, which govern only an actual
// create.
func (c *Client) QuoteCreateServer(ctx context.Context, in *CreateServerInput) (*pricing.GetQuoteOutput, error) {
	const op = "compute.QuoteCreateServer"
	body, err := buildCreateServerBody(op, in)
	if err != nil {
		return nil, err
	}
	body.UserData = ""
	body.UserDataBase64Encoded = false
	info, err := core.QuoteResourceInfo(body)
	if err != nil {
		return nil, err
	}
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceServer,
		Action:       pricing.ActionCreate,
		ResourceInfo: info,
	})
}
