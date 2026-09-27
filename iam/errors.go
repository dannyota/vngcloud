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

// ErrNotSettled is returned by CreatePolicy, UpdatePolicy, CreateGroup, and
// UpdateGroup when their own write reached the server, but the read that
// confirms it and fills in the rest of the Policy or Group failed. The write
// already happened either way: Output still carries the resource's ID.
// CreatePolicy, UpdatePolicy, and CreateGroup leave every other field zero.
// UpdateGroup carries the rest of the group too, as it read it before the
// write, when Name was left nil and that read already ran; otherwise it
// leaves every other field zero, the same as the other three. Either way,
// the caller can look it up again with GetPolicy, list-policies, GetGroup, or
// list-groups.
var ErrNotSettled = errors.New("iam: write accepted but not confirmed by a read")

// ErrManagedPolicy is returned, with no request sent, by UpdatePolicy and
// DeletePolicy when the target policy is managed (Policy.Managed() is
// true): a GreenNode-managed policy can be read and attached but never
// changed or deleted.
var ErrManagedPolicy = errors.New("iam: refused: policy is managed")

// ErrInUse is returned, with no request sent, by DeletePolicy when the
// target policy is attached to a group, an IAM user, or a service account,
// and by DeleteGroup when the target group has a member or an attached
// policy, protected or not either way: the delete would silently remove
// rights from whatever it is attached to, or from every member. The
// sentinel's own text stays generic; the wrapped error names the specific
// reason.
var ErrInUse = errors.New("iam: refused: resource in use")
