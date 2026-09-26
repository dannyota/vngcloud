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

// redactChannelSecretMinLength is the shortest secret value
// redactChannelError will cut out of a message with a plain ReplaceAll. A
// shorter value is likely to also match unrelated text (a short header
// value such as "abc" appearing inside an unrelated word), so cutting it in
// place risks returning a garbled message instead of a safe one.
const redactChannelSecretMinLength = 4

// channelErrorWithheldMessage replaces a channel error's whole message when
// a secret shorter than redactChannelSecretMinLength appears in it: there is
// no way to cut out just that value without risking unrelated text, so the
// message is withheld entirely instead of returned garbled.
const channelErrorWithheldMessage = "server message withheld"

// redactChannelError removes address, every header value, and the whole
// header wire value actually sent, from err's message, when err is a
// *core.APIError. GreenNode's own error messages sometimes echo the request
// back, sometimes with the JSON escaping json.Marshal applies to a
// character such as "&" (encoded "\u0026"), so both the raw and the
// JSON-escaped form of each value are checked. The design requires the SDK,
// not just the CLI, to keep an address, a header value, or the header field
// (any of which can hold a token or other secret) out of an error a caller
// may log or print. An err that is not a *core.APIError, or whose message
// holds none of these values, is returned unchanged; on a match, a new
// *core.APIError is returned with only Message changed, so the original's
// Unwrap chain (and so errors.Is against it) still works.
func redactChannelError(err error, address string, headers []ChannelHeader, headerField string) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	secrets := make([]string, 0, len(headers)+2)
	secrets = append(secrets, address)
	for _, h := range headers {
		secrets = append(secrets, h.Value)
	}
	secrets = append(secrets, headerField)

	msg := apiErr.Message
	for _, secret := range secrets {
		var withhold bool
		msg, withhold = redactChannelSecret(msg, secret)
		if withhold {
			msg = channelErrorWithheldMessage
			break
		}
	}
	if msg == apiErr.Message {
		return err
	}
	redacted := *apiErr
	redacted.Message = msg
	return &redacted
}

// redactChannelSecret removes secret and its JSON-escaped form (json.Marshal
// of secret, with the surrounding quotes stripped, which turns a character
// such as "&" into "\u0026") from msg. The escaped form, when it differs
// from secret, is always safe to cut out in place: the backslash-u-hex
// sequence makes an accidental match in unrelated text vanishingly
// unlikely, regardless of secret's own length. secret itself, when shorter
// than redactChannelSecretMinLength, is never cut out in place; the second
// return value is true when secret still appears in msg after the escaped
// form is removed, telling the caller to withhold the whole message rather
// than risk a garbled partial redaction.
func redactChannelSecret(msg, secret string) (string, bool) {
	if secret == "" {
		return msg, false
	}
	if escaped := jsonEscapedForm(secret); escaped != secret {
		msg = strings.ReplaceAll(msg, escaped, "<redacted>")
	}
	if len(secret) < redactChannelSecretMinLength {
		return msg, strings.Contains(msg, secret)
	}
	return strings.ReplaceAll(msg, secret, "<redacted>"), false
}

// jsonEscapedForm returns secret as json.Marshal would encode it inside a
// JSON string, minus the surrounding quotes. Every value redactChannelError
// passes in is a plain string, so json.Marshal never actually fails here; on
// the error path that cannot be reached, this returns secret unchanged
// rather than adding an error return for a failure that cannot occur.
func jsonEscapedForm(secret string) string {
	data, err := json.Marshal(secret)
	if err != nil || len(data) < 2 {
		return secret
	}
	return string(data[1 : len(data)-1])
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

	headerField := encodeChannelHeaders(in.Headers)
	body := createChannelBody{
		Name:    in.Name,
		Type:    in.Type,
		Address: in.Address,
		Header:  headerField,
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
		return nil, redactChannelError(err, in.Address, in.Headers, headerField)
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
		rawHeader:   resp.Header,
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
// nil resends the channel's current header wire value exactly as read, even
// when it does not decode to [{key,value}] pairs.
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
// unset Name or Address resends the channel's current value, and an unset
// Headers resends the channel's current header wire value exactly as read
// rather than the decoded Headers re-encoded, so a header string that does
// not decode to [{key,value}] pairs is resent unchanged instead of being
// replaced with "". It keeps the channel's Type; there is no way to change
// a channel's type. UpdateChannel accepts only a channel whose current Type
// is ChannelTypeWebhook, and returns core.ErrInvalidInput before any
// request for any other type, until OTP types ship.
//
// The read and the PUT are two separate requests, with no version to check
// in between: a change another caller makes to the channel between them is
// silently overwritten by whichever field values this call resends, the
// same last-write-wins behavior as any other read-then-write pair against
// this API.
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
	if ch.Type != ChannelTypeWebhook {
		return nil, fmt.Errorf("%w: %s: channel %s has type %s, not %s, until OTP types ship",
			core.ErrInvalidInput, op, in.ChannelID, ch.Type, ChannelTypeWebhook)
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
			OTPCode: "",
		},
		OK: []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, redactChannelError(err, address, headers, headerField)
	}

	return &UpdateChannelOutput{Channel: Channel{
		ID:              in.ChannelID,
		Name:            name,
		Type:            ch.Type,
		Address:         address,
		Headers:         headers,
		rawHeader:       headerField,
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
