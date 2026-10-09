package storage

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

type AttachS3KeyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	UserKeyID string `vngcloud:"required"`
	// ServiceAccountID is the IAM service account ID as the iam package
	// returns it, with no "sa-" prefix.
	ServiceAccountID string `vngcloud:"required"`
}

type AttachS3KeyOutput struct{}

type attachS3KeyBody struct {
	ProjectID        string `json:"projectId"`
	ServiceAccountID string `json:"serviceAccountId"`
}

// AttachS3Key restricts an S3 key to a service account. The key loses its
// creator's rights on every bucket and acts as the service account, whose
// rights come only from bucket policies that name its principal (see
// EnsureServiceAccountPrincipal). An attached key can still list the
// project's buckets and create buckets. The data plane follows within
// 3 seconds, and ListS3Keys shows the state as S3Key.SubUserID.
//
// A key is unrestricted from its create until its attach. A caller that
// creates a key to restrict it should delete the key if the attach fails
// for any reason, including an error that says the attach may have
// happened, before it stores the secret.
//
// The server answers every refusal as HTTP 200 with envelope code 114. Each
// is an *APIError with Code "114" and the server's message; none matches a
// sentinel:
//
//	This S3 key is already attached with this service account
//	This S3 key is already attached with another service account
//	StatusCode=404 (the service account does not exist)
//	S3 key not found
//
// The server checks "attached elsewhere" before the service account, so any
// attach of an attached key gives one of the first two messages. The call is
// not idempotent, so it is sent once, with no retry, no resend after a 401,
// and no redirect. A 429 or a failed dial returns an *APIError with
// Retryable true, and the caller may rerun. After a 5xx, a network error, or
// an unreadable response, the error says the change may have happened and
// names list-s3-keys: list the keys and read SubUserID. A rerun that answers
// "already attached with this service account" means the first try took
// effect.
func (c *Client) AttachS3Key(ctx context.Context, in *AttachS3KeyInput) (*AttachS3KeyOutput, error) {
	const op = "storage.AttachS3Key"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserKeyID", in.UserKeyID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	id, err := c.checkKeyPaths(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if _, err := c.exchange(ctx, call{
		op:       op,
		method:   http.MethodPut,
		url:      c.route([]string{"users", "s3_keys", in.UserKeyID, "attach"}, nil),
		regionID: id,
		body:     attachS3KeyBody{ProjectID: in.ProjectID, ServiceAccountID: in.ServiceAccountID},
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
		once:     true,
	}); err != nil {
		return nil, changeMayHaveHappened(err)
	}
	return &AttachS3KeyOutput{}, nil
}

type DetachS3KeyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	UserKeyID string `vngcloud:"required"`
}

type DetachS3KeyOutput struct{}

// DetachS3Key makes an attached S3 key unrestricted again: it has its
// creator's rights on every bucket of the project at once. A key that is not
// attached is refused as envelope code 114 with the message "This S3 key is
// not attached to any service account.", and an unknown key as "S3 key not
// found". Both are *APIError values with no sentinel.
//
// The call is not idempotent and is sent once, as AttachS3Key is. After a
// 5xx, a network error, or an unreadable response, the error says the change
// may have happened and names list-s3-keys. A rerun that answers "not
// attached" means the first try took effect.
func (c *Client) DetachS3Key(ctx context.Context, in *DetachS3KeyInput) (*DetachS3KeyOutput, error) {
	const op = "storage.DetachS3Key"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "UserKeyID", in.UserKeyID); err != nil {
		return nil, err
	}
	id, err := c.checkKeyPaths(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if _, err := c.exchange(ctx, call{
		op:       op,
		method:   http.MethodPut,
		url:      c.route([]string{"users", "s3_keys", in.UserKeyID, "detach"}, nil),
		regionID: id,
		body:     s3KeyBody{ProjectID: in.ProjectID},
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
		once:     true,
	}); err != nil {
		return nil, changeMayHaveHappened(err)
	}
	return &DetachS3KeyOutput{}, nil
}

const changeMayHaveHappenedHint = "; list the keys (list-s3-keys) and read SubUserID to see the state"

// changeMayHaveHappened adds the list-and-read advice to an error that leaves
// an attach or detach unknown: a 5xx, a 2xx the call does not accept, a
// network failure, or a response with no usable body. A refusal that proves
// nothing changed passes through unchanged: a 4xx, an envelope refusal, a
// 429, or a failed dial.
func changeMayHaveHappened(err error) error {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.Retryable {
		return err
	}
	unknown := apiErr.StatusCode == 0 || apiErr.StatusCode >= http.StatusInternalServerError ||
		unacceptedAttachSuccess(apiErr.StatusCode) || apiErr.Code == "EmptyResponse"
	if !unknown {
		return err
	}
	if apiErr.Message == "" {
		apiErr.Message = "request failed"
	}
	if !strings.Contains(apiErr.Message, "may have happened") {
		apiErr.Message += "; the change may have happened"
	}
	apiErr.Message += changeMayHaveHappenedHint
	return err
}

// unacceptedAttachSuccess reports a 2xx status the attach and detach do not
// list as ok. The listed ones reach changeMayHaveHappened only as an envelope
// refusal, which changed nothing.
func unacceptedAttachSuccess(status int) bool {
	return status/100 == 2 && status != http.StatusOK && status != http.StatusNoContent
}
