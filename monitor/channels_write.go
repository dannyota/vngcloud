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
// redactOTPError will cut out of a message with a plain ReplaceAll. A
// shorter value is likely to also match unrelated text (a short header
// value such as "abc" appearing inside an unrelated word), so cutting it in
// place risks returning a garbled message instead of a safe one.
const redactChannelSecretMinLength = 4

// channelErrorWithheldMessage replaces a channel error's whole message when
// a secret shorter than redactChannelSecretMinLength appears in it: there is
// no way to cut out just that value without risking unrelated text, so the
// message is withheld entirely instead of returned garbled.
const channelErrorWithheldMessage = "server message withheld"

// redactOTPError removes address, every header value, the whole header wire
// value actually sent, the OTP, the ref Send OTP returned, and the code
// Validate OTP returned, from err's message, when err is a *core.APIError.
// GreenNode's own error messages sometimes echo the request back, sometimes
// with the JSON escaping json.Marshal applies to a character such as "&"
// (encoded as a backslash-u escape), so both the raw and the JSON-escaped
// form of each value are checked. The design treats every one of these as a
// secret that must never appear in an error a caller may log or print. otp,
// ref, and code are each passed empty when the call that failed never had
// one to send (SendChannelOTP has no ref or code yet; a create or update
// with no OTP set has none of the three); redactChannelSecret already skips
// an empty secret, so passing one through is safe. An err that is not a
// *core.APIError, or whose message holds none of these values, is returned
// unchanged; on a match, a new *core.APIError is returned with only Message
// changed, so the original's Unwrap chain (and so errors.Is against it)
// still works.
func redactOTPError(err error, address string, headers []ChannelHeader, headerField, otp, ref, code string) error {
	secrets := make([]string, 0, len(headers)+5)
	secrets = append(secrets, address)
	for _, h := range headers {
		secrets = append(secrets, h.Value)
	}
	secrets = append(secrets, headerField, otp, ref, code)
	return redactChannelSecrets(err, secrets)
}

// redactChannelSecrets removes each of secrets, in order, from err's
// message, when err is a *core.APIError; see redactChannelSecret for the
// per-secret rule. An err that is not a *core.APIError, or whose message
// holds none of secrets, is returned unchanged; on a match, a new
// *core.APIError is returned with only Message changed, so the original's
// Unwrap chain (and so errors.Is against it) still works.
func redactChannelSecrets(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return err
	}

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
// OTP; OTPRef and OTP are left empty for it, and the server refuses a
// create with no otpCode for any other type. OTPRef and OTP must both be
// set, or both left empty; OTP set with no OTPRef fails with
// core.ErrInvalidInput before any request, since Validate OTP needs both.
type CreateChannelInput struct {
	Name    string `vngcloud:"required"`
	Type    string `vngcloud:"required"`
	Address string `vngcloud:"required"`

	Headers []ChannelHeader
	OTPRef  string
	OTP     string
}

type CreateChannelOutput struct {
	Channel Channel
}

// CreateChannel creates a notification channel. When OTP is set, it first
// calls Validate OTP with OTPRef, OTP, Address, and the encoded Headers; a
// null validated code (a wrong or expired OTP) returns ErrOTPRejected and
// sends no create. It is a POST and is never retried after a failure that
// may have already reached the server: after any error that is not a 4xx
// *core.APIError or core.ErrInvalidInput, the channel may exist, and the
// caller lists channels by Name before creating it again, rather than
// retrying blind. The same no-retry rule applies to the validate step
// itself, since a retry could spend an OTP the first attempt already
// validated. A server error message that echoes Address, a header value,
// the OTP, OTPRef, or the validated code comes back with that value
// replaced by "<redacted>".
func (c *Client) CreateChannel(ctx context.Context, in *CreateChannelInput) (*CreateChannelOutput, error) {
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

	headerField := encodeChannelHeaders(in.Headers)
	otpCode := ""
	if in.OTP != "" {
		code, err := c.validateChannelOTP(ctx, op, in.Address, in.Headers, headerField, in.OTPRef, in.OTP)
		if err != nil {
			return nil, err
		}
		if code == "" {
			return nil, fmt.Errorf("%w: %s: the otp for %s was wrong or expired", ErrOTPRejected, op, in.Type)
		}
		otpCode = code
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
		return nil, redactOTPError(err, in.Address, in.Headers, headerField, in.OTP, in.OTPRef, otpCode)
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
// OTP must both be set, or both left empty; OTP set with no OTPRef fails
// with core.ErrInvalidInput before any request.
type UpdateChannelInput struct {
	ChannelID string `vngcloud:"required"`

	Name    *string
	Address *string
	Headers *[]ChannelHeader
	OTPRef  string
	OTP     string
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
// a channel's type, and UpdateChannel never sends one other than the
// channel's own current Type. When OTP is set, it validates it (see
// validateChannelOTP) before the PUT; a null validated code (a wrong or
// expired OTP) returns ErrOTPRejected and sends no PUT.
//
// The read and the PUT are two separate requests, with no version to check
// in between: a change another caller makes to the channel between them is
// silently overwritten by whichever field values this call resends, the
// same last-write-wins behavior as any other read-then-write pair against
// this API.
//
// The PUT itself is idempotent and keeps the transport's normal retries,
// since resending the same full replacement body is safe; the validate
// step, when it runs, does not, since a retry could spend an OTP the first
// attempt already validated. The PUT's 200 response has no body, so the
// returned Output holds the fields UpdateChannel itself just sent, plus the
// CreatedDate and MetricMappingID the read before it returned; it never
// carries a fresh UpdatedDate, since nothing in this call's own responses
// gives one. A server error message that echoes the sent Address, a header
// value, the OTP, OTPRef, or the validated code comes back with that value
// replaced by "<redacted>".
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
	if in.OTP != "" && in.OTPRef == "" {
		return nil, fmt.Errorf("%w: %s: OTPRef is required when OTP is set", core.ErrInvalidInput, op)
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
		code, err := c.validateChannelOTP(ctx, op, address, headers, headerField, in.OTPRef, in.OTP)
		if err != nil {
			return nil, err
		}
		if code == "" {
			return nil, fmt.Errorf("%w: %s: the otp for channel %s was wrong or expired", ErrOTPRejected, op, in.ChannelID)
		}
		otpCode = code
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
		OK: []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, redactOTPError(err, address, headers, headerField, in.OTP, in.OTPRef, otpCode)
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
