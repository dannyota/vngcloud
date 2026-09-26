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

// redactChannelError replaces every occurrence of address, and of each
// header value, in err's message with "<redacted>", when err is a
// *core.APIError. GreenNode's own error messages sometimes echo the request
// back; the design requires the SDK, not just the CLI, to keep an address
// or a header value (either can hold a token or other secret) out of an
// error that a caller may log or print. An err that is not a *core.APIError,
// or whose message holds neither value, is returned unchanged; on a match,
// a new *core.APIError is returned with only Message changed, so the
// original's Unwrap chain (and so errors.Is against it) still works.
func redactChannelError(err error, address string, headers []ChannelHeader) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	msg := apiErr.Message
	if address != "" {
		msg = strings.ReplaceAll(msg, address, "<redacted>")
	}
	for _, h := range headers {
		if h.Value != "" {
			msg = strings.ReplaceAll(msg, h.Value, "<redacted>")
		}
	}
	if msg == apiErr.Message {
		return err
	}
	redacted := *apiErr
	redacted.Message = msg
	return &redacted
}

// channelDeleteNotFoundSubstring is the lowercased text GreenNode's delete
// endpoint sends in a 400 body for a channel that no longer exists
// ("Notification with id <id> is not found"), unlike every other not-found
// response in this package, which is a 404. mapChannelDeleteNotFound
// matches on this rather than the status alone, so an unrelated 400 (a
// malformed id, say) is not misreported as not-found.
const channelDeleteNotFoundSubstring = "not found"

// mapChannelDeleteNotFound rewraps a 400 APIError from DeleteChannel whose
// message contains channelDeleteNotFoundSubstring into the SDK's ordinary
// not-found sentinel, the same one every other channel operation returns for
// an unknown id. Any other error, including a 400 for a different reason,
// passes through unchanged.
func mapChannelDeleteNotFound(op, channelID string, err error) error {
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(apiErr.Message), channelDeleteNotFoundSubstring) {
		return fmt.Errorf("%w: %s: channel %s", core.ErrNotFound, op, channelID)
	}
	return err
}

// createChannelBody is CreateChannel's request body. OTPCode is always sent
// empty: CreateChannel accepts only ChannelTypeWebhook until OTP channels
// ship, and a webhook needs no OTP.
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

// CreateChannelInput creates a notification channel. Type must be
// ChannelTypeWebhook: every other type needs an OTP, which ships in a later
// release, and the server refuses a create with no otpCode for one. Headers
// left nil or empty sends no header field value, the same as a webhook
// channel with none.
type CreateChannelInput struct {
	Name    string `vngcloud:"required"`
	Type    string `vngcloud:"required"`
	Address string `vngcloud:"required"`

	Headers []ChannelHeader
}

type CreateChannelOutput struct {
	Channel Channel
}

// CreateChannel creates a webhook notification channel. It is a POST and is
// never retried after a failure that may have already reached the server:
// after any error that is not a 4xx *core.APIError or core.ErrInvalidInput,
// the channel may exist, and the caller lists channels by Name before
// creating it again, rather than retrying blind. A server error message
// that echoes Address or a header value comes back with that value replaced
// by "<redacted>".
func (c *Client) CreateChannel(ctx context.Context, in *CreateChannelInput) (*CreateChannelOutput, error) {
	const op = "monitor.CreateChannel"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in.Type != ChannelTypeWebhook {
		return nil, fmt.Errorf("%w: %s accepts only Type %s until OTP channels ship", core.ErrInvalidInput, op, ChannelTypeWebhook)
	}

	body := createChannelBody{
		Name:    in.Name,
		Type:    in.Type,
		Address: in.Address,
		Header:  encodeChannelHeaders(in.Headers),
		OTPCode: "",
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
		return nil, redactChannelError(err, in.Address, in.Headers)
	}
	if resp.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	return &CreateChannelOutput{Channel: Channel{
		ID:          resp.ID,
		Name:        resp.Name,
		Address:     resp.Address,
		Type:        in.Type,
		Headers:     decodeChannelHeaders(resp.Header),
		CreatedDate: resp.CreatedDate,
	}}, nil
}

// updateChannelBody is UpdateChannel's request body: the same shape as
// createChannelBody plus the channel's id, since the API takes id in the
// body rather than the URL. OTPCode is always sent empty; an update that
// changes an OTP channel's Address without one is refused by the server.
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
// nil resends the channel's current ones unchanged.
type UpdateChannelInput struct {
	ChannelID string `vngcloud:"required"`

	Name    *string
	Address *string
	Headers *[]ChannelHeader
}

type UpdateChannelOutput struct {
	Channel Channel
}

// UpdateChannel changes a channel. The API takes a full body and clears any
// field left out of it, so UpdateChannel reads the channel first with
// GetChannel and resends every field the caller left nil unchanged: an
// unset Name, Address, or Headers resends the channel's current value,
// never null or "[]" for a channel that already has none. It keeps the
// channel's Type; there is no way to change a channel's type.
//
// The PUT itself is idempotent and keeps the transport's normal retries,
// since resending the same full replacement body is safe. Its 200 response
// has no body, so the returned Output holds the fields UpdateChannel itself
// just sent, plus the CreatedDate and MetricMappingID the read before it
// returned; it never carries a fresh UpdatedDate, since nothing in this
// call's own responses gives one. A server error message that echoes the
// sent Address or a header value comes back with that value replaced by
// "<redacted>".
func (c *Client) UpdateChannel(ctx context.Context, in *UpdateChannelInput) (*UpdateChannelOutput, error) {
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

	current, err := c.GetChannel(ctx, &GetChannelInput{ChannelID: in.ChannelID})
	if err != nil {
		return nil, err
	}
	ch := current.Channel

	name := ch.Name
	if in.Name != nil {
		name = *in.Name
	}
	address := ch.Address
	if in.Address != nil {
		address = *in.Address
	}
	headers := ch.Headers
	if in.Headers != nil {
		headers = *in.Headers
	}
	if len(headers) == 0 {
		// Normalize a nil or empty Headers to nil, the same "no headers"
		// value decodeChannelHeaders produces for every Channel read, so
		// the Output below matches a fresh GetChannel regardless of
		// whether the caller passed nil or a non-nil empty slice.
		headers = nil
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
			Header:  encodeChannelHeaders(headers),
			OTPCode: "",
		},
		OK: []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, redactChannelError(err, address, headers)
	}

	return &UpdateChannelOutput{Channel: Channel{
		ID:              in.ChannelID,
		Name:            name,
		Type:            ch.Type,
		Address:         address,
		Headers:         headers,
		MetricMappingID: ch.MetricMappingID,
		CreatedDate:     ch.CreatedDate,
	}}, nil
}

// DeleteChannelInput identifies the channel to delete.
type DeleteChannelInput struct {
	ChannelID string `vngcloud:"required"`
}

type DeleteChannelOutput struct{}

// DeleteChannel deletes a channel. Deleting a channel a check still names in
// its Notifications does not fail; the check keeps the now-dangling id.
//
// DELETE is idempotent and keeps the transport's normal retries. Unlike
// every other not-found response in this package, a second delete of the
// same channel comes back as a 400, not a 404; DeleteChannel recognizes that
// specific message and returns the SDK's ordinary not-found sentinel for it,
// the same one core.ErrNotFound wraps everywhere else.
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
