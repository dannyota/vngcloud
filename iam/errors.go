package iam

import (
	"errors"

	"danny.vn/vngcloud/internal/core"
)

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

// ErrCreateUnconfirmed means a create returned a credential or identity but
// could not be confirmed. Preserve any returned secret. Only an attributable,
// non-empty create ID can authorize cleanup; a missing or unsafe ID stays empty.
var ErrCreateUnconfirmed = errors.New("iam: created but not confirmed by a read")

// ErrNotSettled means an accepted write could not be confirmed, or a policy
// replacement may have reached the server. Partial output retains a safe ID
// or generated credential. Check settlement before nested HTTP or context
// errors, and reconcile with reads before another write.
var ErrNotSettled = core.ErrNotSettled

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

// ErrOriginNotIAM refuses a user write when target identity or IAM origin
// cannot be established. No mutation has been sent.
var ErrOriginNotIAM = errors.New("iam: refused: target IAM origin is unproven")

// ErrUserCreateUncertain means creation may have reached the server without
// an attributable ID. A candidate proves no ownership; never retry or clean
// it up automatically. Partial output retains any generated password.
var ErrUserCreateUncertain = errors.New("iam: user create outcome is uncertain")

// ErrUserWriteUncertain means an administration write may have reached the
// server. Never repeat it automatically; retain any generated password.
var ErrUserWriteUncertain = errors.New("iam: user write outcome is uncertain")
