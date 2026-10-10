package compute

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

// createServerWithheldMessage replaces a failing create response's own
// message when Input.UserData is set: the response could otherwise quote
// the cloud-init text, or its base64 form, back in a validation error.
const createServerWithheldMessage = "server message withheld: it may quote user data"

// CreateServerInput creates a server. QuoteCreateServer takes the same
// Input and prices it, per the SDK's paid-write convention that a quote is
// built from the create's own code. A quote requires only ZoneID, FlavorID,
// ImageID, RootDiskSize, and RootDiskTypeID, plus the data disk pair when
// set, and sends encryptionVolume true when either encryption type is set,
// as the console's price request does: the other fields do not change the
// price and never reach the billing gateway. A quote still checks the shape of every field that is set. Every
// ID below, in a path or a body, is checked with core.CheckPathID before
// any request, including the quote.
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
	// RootDiskEncryptionTypeID, optional, is an ID from
	// volume.ListEncryptionTypes. It encrypts the root disk, sends
	// rootDiskEncryptionType, and sets encryptionVolume.
	RootDiskEncryptionTypeID string

	// DataDiskSize and DataDiskTypeID together add one optional data disk;
	// set both or neither.
	DataDiskSize   int
	DataDiskTypeID string
	DataDiskName   string
	// DataDiskEncryptionTypeID, optional, encrypts the data disk like
	// RootDiskEncryptionTypeID. It needs the data disk.
	DataDiskEncryptionTypeID string

	ServerGroupID string

	// UserData is cloud-init text, which can hold secrets such as a
	// bootstrap token. The SDK base64-encodes it and sends
	// userDataBase64Encoded true. It is never sent to QuoteCreateServer's
	// quote. When it is set, CreateServer's own create request is
	// transport.Request.Sensitive and carries both this value and its
	// base64 form as Redact, plus a fixed WithholdMessage, so a failing
	// create's error can never echo it back.
	UserData vngcloud.Secret

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

// createServerBody is CreateServer's request body, built by
// buildCreateServerBody. EncryptionVolume and IsEnableAutoRenew carry no
// omitempty tag, so a false value still reaches the wire: the server
// requires encryptionVolume, and an omitted isEnableAutoRenew must never be
// read as true.
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
	RootDiskEncryption    string   `json:"rootDiskEncryptionType,omitempty"`
	DataDiskEncryption    string   `json:"dataDiskEncryptionType,omitempty"`
	ServerGroupID         string   `json:"serverGroupId,omitempty"`
	UserData              string   `json:"userData,omitempty"`
	UserDataBase64Encoded bool     `json:"userDataBase64Encoded,omitempty"`
	IsEnableAutoRenew     bool     `json:"isEnableAutoRenew"`
}

// serverQuoteBody is the server create quote's resourceInfo: the keys the
// billing gateway prices, as the console sends them. QuoteCreateServer and
// CreateServer's price guard both build it with buildServerQuoteInfo.
// EncryptionVolume has no omitempty tag, so false still reaches the wire.
type serverQuoteBody struct {
	ZoneID           string `json:"zoneId"`
	FlavorID         string `json:"flavorId"`
	ImageID          string `json:"imageId"`
	RootDiskSize     int    `json:"rootDiskSize"`
	RootDiskTypeID   string `json:"rootDiskTypeId"`
	EncryptionVolume bool   `json:"encryptionVolume"`
	DataDiskSize     int    `json:"dataDiskSize,omitempty"`
	DataDiskTypeID   string `json:"dataDiskTypeId,omitempty"`
}

// checkServerShape checks the shape of every field of in that is set, under
// op's name: sizes, the data disk pair, and each ID. A zero RootDiskSize
// passes here; the quote and the create each require their own set first.
func checkServerShape(op string, in *CreateServerInput) error {
	if in.RootDiskSize < 0 {
		return fmt.Errorf("%w: %s: RootDiskSize must be greater than 0, got %d", core.ErrInvalidInput, op, in.RootDiskSize)
	}
	if in.DataDiskSize < 0 {
		return fmt.Errorf("%w: %s: DataDiskSize must not be negative, got %d", core.ErrInvalidInput, op, in.DataDiskSize)
	}
	if (in.DataDiskSize > 0) != (in.DataDiskTypeID != "") {
		return fmt.Errorf("%w: %s requires DataDiskSize and DataDiskTypeID together or neither", core.ErrInvalidInput, op)
	}
	if in.DataDiskEncryptionTypeID != "" && in.DataDiskSize == 0 {
		return fmt.Errorf("%w: %s: DataDiskEncryptionTypeID needs DataDiskSize and DataDiskTypeID", core.ErrInvalidInput, op)
	}
	for _, id := range [...]struct{ field, value string }{
		{"FlavorID", in.FlavorID},
		{"ImageID", in.ImageID},
		{"VPCID", in.VPCID},
		{"SubnetID", in.SubnetID},
		{"SSHKeyID", in.SSHKeyID},
		{"RootDiskTypeID", in.RootDiskTypeID},
		{"ServerGroupID", in.ServerGroupID},
		{"DataDiskTypeID", in.DataDiskTypeID},
	} {
		if id.value == "" {
			continue
		}
		if err := core.CheckPathID(op, id.field, id.value); err != nil {
			return err
		}
	}
	for _, id := range [...]struct{ field, value string }{
		{"RootDiskEncryptionTypeID", in.RootDiskEncryptionTypeID},
		{"DataDiskEncryptionTypeID", in.DataDiskEncryptionTypeID},
	} {
		if id.value == "" {
			continue
		}
		if err := core.CheckTypeID(op, id.field, id.value); err != nil {
			return err
		}
	}
	for i, sgID := range in.SecurityGroupIDs {
		if err := core.CheckPathID(op, fmt.Sprintf("SecurityGroupIDs[%d]", i), sgID); err != nil {
			return err
		}
	}
	return nil
}

// encryptsAnyDisk reports whether in asks for an encrypted disk, which sets
// encryptionVolume on the quote and the create.
func encryptsAnyDisk(in *CreateServerInput) bool {
	return in.RootDiskEncryptionTypeID != "" || in.DataDiskEncryptionTypeID != ""
}

// buildServerQuoteInfo validates the priced fields of in and builds the
// quote's resourceInfo from them alone. QuoteCreateServer and CreateServer's
// price guard both call it, so the price a caller sees is the price the
// guard checks (ADR 0002 rule 8). The body type has no field for user data,
// which is not priced and must never reach the billing gateway.
func buildServerQuoteInfo(op string, in *CreateServerInput) (map[string]any, error) {
	if err := core.CheckRequiredFields(op, in, "ZoneID", "FlavorID", "ImageID", "RootDiskSize", "RootDiskTypeID"); err != nil {
		return nil, err
	}
	if err := checkServerShape(op, in); err != nil {
		return nil, err
	}
	return core.QuoteResourceInfo(serverQuoteBody{
		ZoneID:           in.ZoneID,
		FlavorID:         in.FlavorID,
		ImageID:          in.ImageID,
		RootDiskSize:     in.RootDiskSize,
		RootDiskTypeID:   in.RootDiskTypeID,
		EncryptionVolume: encryptsAnyDisk(in),
		DataDiskSize:     in.DataDiskSize,
		DataDiskTypeID:   in.DataDiskTypeID,
	})
}

// buildCreateServerBody validates in and builds the body CreateServer
// sends. It requires every field tagged vngcloud:"required". It never sets
// attachFloating, userName, userPassword, osLicence, or expirePassword:
// this SDK has no field for a public IP or password login on create.
func buildCreateServerBody(op string, in *CreateServerInput) (createServerBody, error) {
	if err := core.CheckRequired(op, in); err != nil {
		return createServerBody{}, err
	}
	if len(in.SecurityGroupIDs) == 0 {
		return createServerBody{}, fmt.Errorf("%w: %s requires at least one SecurityGroupIDs entry", core.ErrInvalidInput, op)
	}
	if err := checkServerShape(op, in); err != nil {
		return createServerBody{}, err
	}

	body := createServerBody{
		Name:               in.Name,
		ZoneID:             in.ZoneID,
		FlavorID:           in.FlavorID,
		ImageID:            in.ImageID,
		NetworkID:          in.VPCID,
		SubnetID:           in.SubnetID,
		SecurityGroup:      in.SecurityGroupIDs,
		SSHKeyID:           in.SSHKeyID,
		RootDiskSize:       in.RootDiskSize,
		RootDiskTypeID:     in.RootDiskTypeID,
		EncryptionVolume:   encryptsAnyDisk(in),
		DataDiskName:       in.DataDiskName,
		DataDiskSize:       in.DataDiskSize,
		DataDiskTypeID:     in.DataDiskTypeID,
		RootDiskEncryption: in.RootDiskEncryptionTypeID,
		DataDiskEncryption: in.DataDiskEncryptionTypeID,
		ServerGroupID:      in.ServerGroupID,
		IsEnableAutoRenew:  in.AutoRenew,
	}
	if in.UserData.Reveal() != "" {
		body.UserData = base64.StdEncoding.EncodeToString([]byte(in.UserData.Reveal()))
		body.UserDataBase64Encoded = true
	}
	return body, nil
}

// QuoteCreateServer prices the server Input would create, without ordering
// it. It requires and sends only the fields that change the price (see
// CreateServerInput) and checks the shape of every other field that is set,
// so a bad ID fails here as it will at the create. UserData never reaches
// the billing gateway. It ignores Input.MaxPrice and Input.NoWait, which
// govern only an actual create.
func (c *Client) QuoteCreateServer(ctx context.Context, in *CreateServerInput) (*pricing.GetQuoteOutput, error) {
	const op = "compute.QuoteCreateServer"
	info, err := buildServerQuoteInfo(op, in)
	if err != nil {
		return nil, err
	}
	return c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceServer,
		Action:       pricing.ActionCreate,
		ResourceInfo: info,
	})
}

// CreateServerOutput is CreateServer's result. Server is filled from
// CreateServer's own post-create wait unless NoWait is set, in which case it
// holds only the UUID and Name the caller gave, since the create response's
// other fields are not confirmed live. MonthlyPrice is the quote's own
// OptimumPrice, the price actually checked against Input.MaxPrice.
type CreateServerOutput struct {
	Server       Server
	MonthlyPrice float64
}

// createServerResponse decodes CreateServer's response. Its only confirmed
// field is data.uuid; every other field is unverified, so this never
// decodes into the bare Server model.
type createServerResponse struct {
	Data struct {
		UUID string `json:"uuid"`
	} `json:"data"`
}

// CreateServer orders a server. Before any request, it rejects a NaN,
// +Inf, -Inf, or negative Input.MaxPrice with core.ErrInvalidInput: the
// price guard below (quote.OptimumPrice > Input.MaxPrice) cannot compare
// any of those safely. It then lists every server and refuses, also with
// core.ErrInvalidInput and before any pricing or order request, when one
// already exists with Input.Name exactly, so a rerun after an unclear
// failure never risks ordering a second server under the same name.
//
// CreateServer builds the order body from Input with buildCreateServerBody
// and its price guard's quote body with buildServerQuoteInfo (ADR 0002
// rule 8), the same builder QuoteCreateServer uses, so the guard prices the
// request the quote command prints. That body holds only priced fields:
// user data is not priced and never reaches the billing gateway. It refuses with vngcloud.ErrPriceAboveMax,
// ordering nothing, when the quote's OptimumPrice exceeds Input.MaxPrice
// (default 0).
//
// The order is sent with transport.Request.Once (ADR 0003) and is never
// retried after a failure that may have already reached the server: Once
// also stops a 401 from being retried with a refreshed token and stops
// net/http from replaying a 307 or 308 redirect's method and body at the
// Location it names, either of which would otherwise resend this create.
// After any error that is not a 4xx *core.APIError, the server may exist,
// and the caller lists servers and matches Name exactly before ordering
// again. When Input.UserData is set, the request is
// transport.Request.Sensitive, so a decode failure never quotes the
// response body, and it carries Redact (the plain and base64 forms of
// UserData) and WithholdMessage, so a failing response's error can never
// echo it back either. UserData itself is never sent to the quote,
// captured, logged, or echoed in any error.
//
// Without NoWait, CreateServer waits up to 15 minutes, polling GetServer
// every 5 seconds, for the new server to reach ACTIVE. If the server
// reaches ERROR instead, or the bound runs out, or a read or a sleep in
// that wait fails, such as from a canceled ctx, the returned error wraps
// ErrFailed or ErrNotSettled and Output.Server still holds the last server
// a read returned, falling back to one with only the new UUID and
// Input.Name if no read ever succeeded. NoWait skips that wait and returns
// at once with Output.Server holding only the new UUID and Input.Name.
func (c *Client) CreateServer(ctx context.Context, in *CreateServerInput) (*CreateServerOutput, error) {
	const op = "compute.CreateServer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckMaxPrice(op, in.MaxPrice); err != nil {
		return nil, err
	}
	body, err := buildCreateServerBody(op, in)
	if err != nil {
		return nil, err
	}
	info, err := buildServerQuoteInfo(op, in)
	if err != nil {
		return nil, err
	}
	if err := c.refuseIfServerNameExists(ctx, op, in.Name); err != nil {
		return nil, err
	}

	quote, err := c.pricing.GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceServer,
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

	// UserData can hold secrets, such as a bootstrap token. Sensitive keeps
	// the response out of the configured capture hook; Redact scrubs both
	// its plain and base64 forms from a failing response's Message and
	// Code, and WithholdMessage replaces that Message outright, as defense
	// in depth for whatever form Redact's exact-match scrubbing might miss.
	userData := in.UserData.Reveal()
	var redact []string
	var withhold string
	if userData != "" {
		redact = []string{userData, body.UserData}
		withhold = createServerWithheldMessage
	}

	var resp createServerResponse
	req := transport.Request{
		Operation:       op,
		Method:          http.MethodPost,
		URL:             c.computeURL("v2", []string{projectID, "servers"}, nil),
		Body:            body,
		OK:              []int{202},
		Sensitive:       userData != "",
		Redact:          redact,
		WithholdMessage: withhold,
		// Once (ADR 0003): without it, a 401 would be retried once with a
		// refreshed token, and net/http would replay a 307 or 308 redirect's
		// method and body at the Location it names, either sending this
		// create a second time. A create must never be resent.
		Once: true,
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousServerCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the server may exist, list servers and match the name exactly before creating it again"}
	}

	fallback := Server{UUID: resp.Data.UUID, Name: in.Name}
	if in.NoWait {
		return &CreateServerOutput{Server: fallback, MonthlyPrice: quote.OptimumPrice}, nil
	}
	settled, waitErr := c.waitServerActive(ctx, op, resp.Data.UUID)
	if settled == nil {
		settled = &fallback
	}
	return &CreateServerOutput{Server: *settled, MonthlyPrice: quote.OptimumPrice}, waitErr
}

// wrapAmbiguousServerCreateErr wraps err, from the create POST just sent,
// with a hint to list servers before creating again, unless err is already a
// 4xx *core.APIError: a 4xx means the server rejected the request outright,
// so nothing was created and the exact same call is safe to retry. Any
// other error leaves whether the server was created unknown.
func wrapAmbiguousServerCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list servers and match the name exactly before creating it again: %w", op, err)
}

// refuseIfServerNameExists refuses to order a server named name when one
// already exists on the account, before any pricing or order request. It
// walks every page ListServers has, since it has no name filter of its own,
// and fails closed, refusing the create, when the account's own page
// metadata cannot prove the walk saw every server.
func (c *Client) refuseIfServerNameExists(ctx context.Context, op, name string) error {
	return core.CheckNoDuplicateName(op, "server", name,
		func(s Server) string { return s.Name },
		func(page int) (*core.PagedList[Server], error) {
			return c.ListServers(ctx, &ListServersInput{Page: page, Size: core.DefaultPageSize})
		})
}
