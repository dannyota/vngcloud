package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

type EnsureServiceAccountPrincipalInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	// ServiceAccountID is the IAM service account ID as the iam package
	// returns it, with no "sa-" prefix.
	ServiceAccountID string `vngcloud:"required"`
}

// EnsureServiceAccountPrincipalOutput holds the service account's storage
// sub-user. PrincipalARN is the value a bucket policy puts in its Principal.
type EnsureServiceAccountPrincipalOutput struct {
	// SubUserID is "<account user>:sa-<service account name>".
	SubUserID string
	// PrincipalARN is "arn:aws:iam:::user/" followed by SubUserID.
	PrincipalARN string
}

const principalARNPrefix = "arn:aws:iam:::user/"

// EnsureServiceAccountPrincipal makes the storage sub-user of a service
// account if it does not exist, and returns its SubUserID and PrincipalARN.
// A bucket policy names a service account by that principal, so call this
// before writing a policy that names it.
//
// It is a write although it sends a GET: the server creates the sub-user when
// the request carries generated=true, and no read in this package sends it.
// It is idempotent, so a repeat returns the same sub-user and the transport
// may retry it. The sub-user cannot be deleted through the console API. It
// costs nothing and has no rights until a bucket policy names it.
//
// The sub-user is named from the service account's name, not its ID. A new
// service account with the name of a deleted one may get the same principal
// and so inherit any bucket policy that still names it: remove a service
// account from its bucket policies before deleting it.
//
// A response whose subUserId is null, empty, or not exactly
// "<user>:sa-<name>" is an error and returns no Output, so a caller never puts
// the IAM user's own ":iam-" principal in a policy.
func (c *Client) EnsureServiceAccountPrincipal(ctx context.Context, in *EnsureServiceAccountPrincipalInput) (*EnsureServiceAccountPrincipalOutput, error) {
	const op = "storage.EnsureServiceAccountPrincipal"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServiceAccountID", in.ServiceAccountID); err != nil {
		return nil, err
	}
	id, err := c.checkKeyPaths(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return nil, err
	}
	env, err := c.exchange(ctx, call{
		op:     op,
		method: http.MethodGet,
		url: c.route([]string{"users", "details"}, url.Values{
			"generated":      {"true"},
			"project_id":     {in.ProjectID},
			"iam_account_id": {"sa-" + in.ServiceAccountID},
		}),
		regionID: id,
		ok:       []int{http.StatusOK},
		write:    true,
	})
	if err != nil {
		return nil, err
	}
	var data struct {
		SubUserID *string `json:"subUserId"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.SubUserID == nil || *data.SubUserID == "" {
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: http.StatusOK,
			Code:       "NoPrincipal",
			Message:    "the response had no subUserId; no principal was made",
		}
	}
	sub := *data.SubUserID
	user, rest, ok := strings.Cut(sub, ":")
	if !ok || user == "" || !strings.HasPrefix(rest, "sa-") || len(rest) <= len("sa-") || strings.Contains(rest, ":") {
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: http.StatusOK,
			Code:       "NotServiceAccountPrincipal",
			Message:    "the response subUserId is not a service account sub-user (not \"<user>:sa-<name>\"); it is not returned",
		}
	}
	return &EnsureServiceAccountPrincipalOutput{SubUserID: sub, PrincipalARN: principalARNPrefix + sub}, nil
}
