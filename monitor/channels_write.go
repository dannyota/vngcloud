package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// encodeChannelHeaders JSON-encodes headers into the header wire field's
// shape, the same one decodeChannelHeaders reads back: an empty string for
// no headers, never "[]" or "null", so a webhook channel with no headers
// round-trips through a read and back out through a write. ChannelHeader
// holds only string fields, so json.Marshal never actually fails for it; on
// the error path that cannot be reached, this returns "" rather than adding
// an error return to every caller for a failure that cannot occur.
func encodeChannelHeaders(headers []ChannelHeader) string {
	if len(headers) == 0 {
		return ""
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return ""
	}
	return string(data)
}

func redactChannelSecrets(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		if errors.Is(err, ErrOTPRejected) {
			return ErrOTPRejected
		}
		if monitorReadSDKError(err) {
			return err
		}
		return fmt.Errorf("monitor: request failed; details withheld: %w", monitorReadCause(err))
	}

	redacted := *apiErr
	redacted.Code = transport.RedactErrorText(redacted.Code, secrets...)
	redacted.Message = transport.RedactErrorText(redacted.Message, secrets...)
	redacted.Err = monitorReadCause(apiErr.Err)
	return &redacted
}

// channelDeleteNotFoundPhrase is the lowercased text GreenNode's delete
// endpoint sends in a 400 body for a channel that no longer exists
// ("Notification with id <id> is not found"), unlike every other not-found
// response in this package, which is a 404. mapChannelDeleteNotFound
// requires this phrase together with the channel's own ID, so an unrelated
// 400 not-found response (a different resource, or a different channel ID
// from a stale retry) is not misreported as this channel's not-found.
const channelDeleteNotFoundPhrase = "notification with id"

// mapChannelDeleteNotFound rewraps a 400 APIError from DeleteChannel, whose
// message contains both channelDeleteNotFoundPhrase and channelID, into the
// SDK's ordinary not-found sentinel, the same one every other channel
// operation returns for an unknown id. The original *core.APIError stays in
// the returned error's chain, so errors.As against it still works alongside
// errors.Is against core.ErrNotFound. Any other error, including a 400 for a
// different reason or naming a different channel, passes through unchanged.
func mapChannelDeleteNotFound(op, channelID string, err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
		lower := strings.ToLower(apiErr.Message)
		if strings.Contains(lower, channelDeleteNotFoundPhrase) && strings.Contains(lower, strings.ToLower(channelID)) {
			return fmt.Errorf("%w: %s: channel %s: %w", core.ErrNotFound, op, channelID, apiErr)
		}
	}
	return err
}

// channelValidTypes are the notification types CreateChannel and
// UpdateChannel accept, matching ListChannelTypes' console-offered set; the
// console no longer offers Teams for a new channel, even though a channel
// made before that change can still read back with it.
var channelValidTypes = map[string]bool{
	ChannelTypeEmail:    true,
	ChannelTypeSlack:    true,
	ChannelTypeSMS:      true,
	ChannelTypeTelegram: true,
	ChannelTypeWebhook:  true,
}

// createChannelBody is CreateChannel's request body. OTPCode is empty for a
// Webhook create, which needs no OTP, and otherwise carries the code
// Validate OTP returned for the caller's OTPRef and OTP.
type createChannelBody struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	Header  string `json:"header"`
	OTPCode string `json:"otpCode"`
}

// createChannelResponse is Create's 200 response, which carries no type: the
// server already knows it from the request, so CreateChannel fills
// Channel.Type from the Input instead of the response.
type createChannelResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Header      string `json:"header"`
	CreatedDate string `json:"createdDate"`
}

// CreateChannelInput creates a notification channel. Type must be Email,
// Slack, SMS, Telegram, or Webhook. Headers left nil or empty sends no
// header field value, the same as a webhook channel with none.
//
// Email, Slack, SMS, and Telegram need an OTP: call SendChannelOTP first,
// then set OTPRef to its Ref and OTP to the code read from the address, and
// CreateChannel validates it before creating the channel. Webhook needs no
// OTP; OTPRef and OTP are left empty for it, and both CreateChannel and the
// server refuse a create with either one set. Every other type is refused
// with no otpCode. OTPRef and OTP must both be set, or both left empty;
// either one set with the other empty fails with core.ErrInvalidInput
// before any request, since Validate OTP needs both.
type CreateChannelInput struct {
	// Name is required.
	Name string `vngcloud:"required"`
	// Type must be Email, Slack, SMS, Telegram, or Webhook.
	Type string `vngcloud:"required"`
	// Address is required.
	Address string `vngcloud:"required"`

	// Headers sends no header value when nil or empty.
	Headers []ChannelHeader
	// OTPRef must be set with OTP for channel types that need an OTP.
	OTPRef string
	// OTP must be set with OTPRef for channel types that need an OTP.
	OTP string
}

// CreateChannelOutput holds the result of CreateChannel.
type CreateChannelOutput struct {
	Channel Channel
}

// CreateChannel validates a supplied OTP and creates a notification channel.
// After an uncertain result, read channels before deciding whether to create again.
func (c *Client) CreateChannel(ctx context.Context, in *CreateChannelInput) (out *CreateChannelOutput, err error) {
	const op = "monitor.CreateChannel"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if !channelValidTypes[in.Type] {
		return nil, fmt.Errorf("%w: %s: Type must be Email, Slack, SMS, Telegram, or Webhook, got %q", core.ErrInvalidInput, op, in.Type)
	}
	if in.OTP != "" && in.OTPRef == "" {
		return nil, fmt.Errorf("%w: %s: OTPRef is required when OTP is set", core.ErrInvalidInput, op)
	}
	if in.OTPRef != "" && in.OTP == "" {
		return nil, fmt.Errorf("%w: %s: OTP is required when OTPRef is set", core.ErrInvalidInput, op)
	}
	if in.Type == ChannelTypeWebhook && (in.OTP != "" || in.OTPRef != "") {
		return nil, fmt.Errorf("%w: %s: Type Webhook takes no OTPRef or OTP", core.ErrInvalidInput, op)
	}

	headerField := encodeChannelHeaders(in.Headers)
	ctx, privacy := monitorPrivateContext(ctx, channelSecrets(in.Address, in.Headers, headerField, in.OTP, in.OTPRef)...)
	defer func() { err = redactChannelSecrets(err, privacy.values) }()
	otpCode := ""
	if in.OTP != "" {
		code, err := c.validateChannelOTP(ctx, op, in.Address, headerField, in.OTPRef, in.OTP)
		if err != nil {
			return nil, err
		}
		if code == "" {
			return nil, fmt.Errorf("%w: %s: the otp for %s was wrong or expired", ErrOTPRejected, op, in.Type)
		}
		otpCode = code
		privacy.add(code)
	}

	body := createChannelBody{
		Name:    in.Name,
		Type:    in.Type,
		Address: in.Address,
		Header:  headerField,
		OTPCode: otpCode,
	}
	var resp createChannelResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.notificationRoute([]string{"notification"}, nil),
		Body:      body,
		OK:        []int{200},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, err
	}
	if resp.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	channel := Channel{
		ID:          resp.ID,
		Name:        resp.Name,
		Address:     resp.Address,
		Type:        in.Type,
		Headers:     decodeChannelHeaders(resp.Header),
		rawHeader:   resp.Header,
		CreatedDate: resp.CreatedDate,
	}
	if err := privacy.channel(op, status, channel); err != nil {
		return nil, err
	}
	return &CreateChannelOutput{Channel: channel}, nil
}

// updateChannelBody is UpdateChannel's request body: the same shape as
// createChannelBody plus the channel's id, since the API takes id in the
// body rather than the URL. OTPCode is empty unless the caller set OTP, in
// which case it carries the code Validate OTP returned; an update that
// changes an OTP channel's Address with no otpCode is refused by the
// server.
type updateChannelBody struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	Header  string `json:"header"`
	OTPCode string `json:"otpCode"`
}

// UpdateChannelInput changes a channel's Name, Address, Headers, or any
// combination, leaving every field left nil unchanged. At least one of Name,
// Address, and Headers must be set. Headers set to a non-nil empty slice
// sends an explicit empty header list, clearing every header; Headers left
// nil resends the channel's current header wire value exactly as read, even
// when it does not decode to [{key,value}] pairs.
//
// A channel whose type needs an OTP (Email, Slack, SMS, or Telegram) needs
// a fresh one to change its Address; the server enforces that, not the SDK.
// Call SendChannelOTP first, then set OTPRef to its Ref and OTP to the code
// read from the address, the same way CreateChannel takes them. OTPRef and
// OTP must both be set, or both left empty; either one set with the other
// empty fails with core.ErrInvalidInput before any request. A Webhook
// channel takes neither; UpdateChannel refuses one set for it once
// GetChannel's read shows the channel's type, before validating anything.
type UpdateChannelInput struct {
	// ChannelID is required; use ASCII letters, digits, or hyphens.
	ChannelID string `vngcloud:"required"`

	// Name keeps the current name when nil.
	Name *string
	// Address keeps the current address when nil.
	Address *string
	// Headers keeps current headers when nil; an empty slice clears them.
	Headers *[]ChannelHeader
	// OTPRef must be set together with OTP or left empty with it.
	OTPRef string
	// OTP must be set together with OTPRef or left empty with it.
	OTP string
}

// UpdateChannelOutput holds the result of UpdateChannel.
type UpdateChannelOutput struct {
	Channel Channel
}

// UpdateChannel reads the channel and merges set fields into its full body.
// A PUT carrying an OTP code is sent once. Confirm uncertain writes with reads.
func (c *Client) UpdateChannel(ctx context.Context, in *UpdateChannelInput) (out *UpdateChannelOutput, err error) {
	const op = "monitor.UpdateChannel"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ChannelID", in.ChannelID); err != nil {
		return nil, err
	}
	if in.Name == nil && in.Address == nil && in.Headers == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}
	if in.OTP != "" && in.OTPRef == "" {
		return nil, fmt.Errorf("%w: %s: OTPRef is required when OTP is set", core.ErrInvalidInput, op)
	}
	if in.OTPRef != "" && in.OTP == "" {
		return nil, fmt.Errorf("%w: %s: OTP is required when OTPRef is set", core.ErrInvalidInput, op)
	}

	var inputAddress string
	var inputHeaders []ChannelHeader
	if in.Address != nil {
		inputAddress = *in.Address
	}
	if in.Headers != nil {
		inputHeaders = *in.Headers
	}
	ctx, privacy := monitorPrivateContext(ctx, channelSecrets(inputAddress, inputHeaders, encodeChannelHeaders(inputHeaders), in.OTP, in.OTPRef)...)
	defer func() { err = redactChannelSecrets(err, privacy.values) }()
	current, err := c.GetChannel(ctx, &GetChannelInput{ChannelID: in.ChannelID})
	if err != nil {
		return nil, err
	}
	ch := current.Channel
	privacy.add(channelSecrets(ch.Address, ch.Headers, ch.rawHeader, "", "")...)
	if err := privacy.channel(op, http.StatusOK, ch); err != nil {
		return nil, err
	}
	// The type is only known once the read above returns, so the Webhook
	// check waits until here; it still runs before validateChannelOTP
	// below, since Webhook needs no OTP and must never spend one.
	if ch.Type == ChannelTypeWebhook && (in.OTP != "" || in.OTPRef != "") {
		return nil, fmt.Errorf("%w: %s: channel %s is Webhook, which takes no OTPRef or OTP", core.ErrInvalidInput, op, in.ChannelID)
	}

	name := ch.Name
	if in.Name != nil {
		name = *in.Name
	}
	address := ch.Address
	if in.Address != nil {
		address = *in.Address
	}

	// headers is the Output's decoded value; headerField is the wire value
	// the PUT actually sends. Left at ch.rawHeader when the caller leaves
	// Headers nil, so a header string that does not decode is resent
	// exactly as read instead of being re-encoded from ch.Headers (nil for
	// an undecodable string) into "".
	headers := ch.Headers
	headerField := ch.rawHeader
	if in.Headers != nil {
		headers = *in.Headers
		if len(headers) == 0 {
			// Normalize a non-nil empty Headers to nil, the same "no
			// headers" value decodeChannelHeaders produces for every
			// Channel read, so the Output below matches a fresh
			// GetChannel regardless of whether the caller passed nil or
			// a non-nil empty slice.
			headers = nil
		}
		headerField = encodeChannelHeaders(headers)
	}

	otpCode := ""
	if in.OTP != "" {
		code, err := c.validateChannelOTP(ctx, op, address, headerField, in.OTPRef, in.OTP)
		if err != nil {
			return nil, err
		}
		if code == "" {
			return nil, fmt.Errorf("%w: %s: the otp for channel %s was wrong or expired", ErrOTPRejected, op, in.ChannelID)
		}
		otpCode = code
		privacy.add(code)
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.notificationRoute([]string{"notification"}, nil),
		Body: updateChannelBody{
			ID:      in.ChannelID,
			Name:    name,
			Type:    ch.Type,
			Address: address,
			Header:  headerField,
			OTPCode: otpCode,
		},
		OK:   []int{200},
		Once: otpCode != "",
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	channel := Channel{
		ID:              in.ChannelID,
		Name:            name,
		Type:            ch.Type,
		Address:         address,
		Headers:         headers,
		rawHeader:       headerField,
		MetricMappingID: ch.MetricMappingID,
		CreatedDate:     ch.CreatedDate,
	}
	if err := privacy.channel(op, http.StatusOK, channel); err != nil {
		return nil, err
	}
	return &UpdateChannelOutput{Channel: channel}, nil
}

// DeleteChannelInput identifies the channel to delete.
type DeleteChannelInput struct {
	// ChannelID is required; use ASCII letters, digits, or hyphens.
	ChannelID string `vngcloud:"required"`
}

// DeleteChannelOutput carries no result fields.
type DeleteChannelOutput struct{}

// DeleteChannel deletes a channel. Deleting a channel a check still names in
// its Notifications does not fail; the server itself strips the deleted
// channel's ID from every check's Notifications, removing that channel's
// alerting from every check that used it, rather than leaving a dangling id.
//
// DELETE is idempotent and keeps the transport's normal retries: a retry
// whose first attempt already reached the server sees the channel gone and
// gets the same not-found sentinel as a genuine second delete, which the
// caller treats as done. Unlike every other not-found response in this
// package, a second delete of the same channel comes back as a 400, not a
// 404; DeleteChannel recognizes that specific message and returns the SDK's
// ordinary not-found sentinel for it, the same one core.ErrNotFound wraps
// everywhere else.
func (c *Client) DeleteChannel(ctx context.Context, in *DeleteChannelInput) (*DeleteChannelOutput, error) {
	const op = "monitor.DeleteChannel"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ChannelID", in.ChannelID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.notificationRoute([]string{"notification", in.ChannelID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, mapChannelDeleteNotFound(op, in.ChannelID, err)
	}
	return &DeleteChannelOutput{}, nil
}
