package iam

import "errors"

// ErrSelfChange is returned, with no request sent, when a guarded write's
// target is the caller itself, such as a service account resetting its own
// secret, or when the caller is a service account and the write targets any
// service account at all: GetCallerIdentity's UserID for a service-account
// caller is not confirmed to use the same ID form as a target's ID or
// ClientID, so the guard cannot safely rule out a self-change and refuses
// every case. It wins over ErrPrivilegedChange when both would apply.
var ErrSelfChange = errors.New("iam: refused: target is the caller")

// ErrPrivilegedChange is returned, with no request sent, when a guarded
// write's target is a protected principal: one that holds, or would gain,
// an IAM write right. See the design's guard rules for exactly which writes
// this covers.
var ErrPrivilegedChange = errors.New("iam: refused: target is protected")

// ErrNoSecret is returned by CreateServiceAccount and
// ResetServiceAccountSecret when their response reports success but carries
// no client secret. The write already reached the server either way:
// CreateServiceAccount still returns the created ServiceAccount, and
// ResetServiceAccountSecret has likely already rotated the secret without
// returning it. Either way, run ResetServiceAccountSecret to get a usable
// value.
var ErrNoSecret = errors.New("iam: no client secret returned")

// ErrCreateUnconfirmed is returned by CreateServiceAccount, together with a
// non-nil Output holding the new service account's ID and whatever client
// secret the create response carried, when the create itself succeeded but
// the read-back that fills in the rest of the ServiceAccount fields failed.
// The account is real either way: the caller can still write --secret-file
// from Output.ClientSecret, or delete the account by Output.ServiceAccount.ID.
var ErrCreateUnconfirmed = errors.New("iam: service account created but not confirmed by a read")
