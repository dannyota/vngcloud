package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ErrOTPRejected means Validate OTP returned a null code: the OTP the
// caller sent CreateChannel or UpdateChannel was wrong or expired. Neither
// call sends a create or update request in this case.
var ErrOTPRejected = errors.New("monitor: otp rejected")

// channelOTPTypes are the notification types Send OTP messages: every type
// but Webhook, which needs no OTP. SendChannelOTP refuses any other Type,
// including Webhook, before any request.
var channelOTPTypes = map[string]bool{
	ChannelTypeEmail:    true,
	ChannelTypeSlack:    true,
	ChannelTypeSMS:      true,
	ChannelTypeTelegram: true,
}

// sendOTPBody is SendChannelOTP's request body.
type sendOTPBody struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	Header  string `json:"header"`
}

// SendChannelOTPInput starts the OTP flow CreateChannel and UpdateChannel
// need for every channel type but Webhook. Headers, when the caller means
// to create or update a channel that also carries headers, is encoded the
// same way CreateChannel and UpdateChannel encode theirs, so the caller
// passes the same value to both calls.
type SendChannelOTPInput struct {
	Type    string `vngcloud:"required"`
	Address string `vngcloud:"required"`

	Headers []ChannelHeader
}

type SendChannelOTPOutput struct {
	// Ref identifies this OTP to the validate step CreateChannel or
	// UpdateChannel performs internally when the caller sets OTPRef and OTP.
	Ref string
	// ExpiresAt is when the OTP stops being valid.
	ExpiresAt time.Time
}

// SendChannelOTP messages Address with a one-time code, for every channel
// type but Webhook, which needs none; Type outside that set, Webhook
// included, is refused before any request. It is a POST that messages the
// address, so it is never retried after a failure that may have already
// reached the server: a retry could send a second message, and a caller
// that wants one anyway calls SendChannelOTP again itself. A server error
// message that echoes Address or a header value comes back with that value
// replaced by "<redacted>".
func (c *Client) SendChannelOTP(ctx context.Context, in *SendChannelOTPInput) (*SendChannelOTPOutput, error) {
	const op = "monitor.SendChannelOTP"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if !channelOTPTypes[in.Type] {
		return nil, fmt.Errorf("%w: %s: Type must be Email, Slack, SMS, or Telegram, got %q", core.ErrInvalidInput, op, in.Type)
	}

	headerField := encodeChannelHeaders(in.Headers)
	var resp struct {
		Ref string `json:"ref"`
		// ExpiredAt is epoch milliseconds; a float64 target decodes both a
		// plain integer and an integral decimal such as 1.7e12, the same
		// tolerance flexibleInt applies to other numeric fields in this
		// package, without erroring on whichever shape the server sends.
		ExpiredAt float64 `json:"expiredAt"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.notificationRoute([]string{"notification", "otps"}, nil),
		Body:      sendOTPBody{Type: in.Type, Address: in.Address, Header: headerField},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, redactOTPError(err, in.Address, in.Headers, headerField, "", "", "")
	}
	return &SendChannelOTPOutput{Ref: resp.Ref, ExpiresAt: time.UnixMilli(int64(resp.ExpiredAt))}, nil
}

// validateOTPBody is Validate OTP's request body, sent internally by
// CreateChannel and UpdateChannel. There is no public method for it: the
// design exposes the OTP flow as SendChannelOTP plus the OTPRef and OTP
// fields a create or update takes.
type validateOTPBody struct {
	OTP     string `json:"otp"`
	Address string `json:"address"`
	Ref     string `json:"ref"`
	Header  string `json:"header"`
}

// validateChannelOTP calls Validate OTP and returns the code the server
// validated, or an empty string when the OTP was wrong or expired: the
// server's own signal, a null code. op names the caller (monitor.CreateChannel
// or monitor.UpdateChannel) in every error this produces, including a
// redacted one. It is a POST that may spend the OTP, so it is never retried
// after a failure that may have already reached the server.
func (c *Client) validateChannelOTP(ctx context.Context, op, address string, headers []ChannelHeader, headerField, ref, otp string) (string, error) {
	var resp struct {
		Code *string `json:"code"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.notificationRoute([]string{"notification", "otps", "validate"}, nil),
		Body:      validateOTPBody{OTP: otp, Address: address, Ref: ref, Header: headerField},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return "", redactOTPError(err, address, headers, headerField, otp, ref, "")
	}
	if resp.Code == nil {
		return "", nil
	}
	return *resp.Code, nil
}
