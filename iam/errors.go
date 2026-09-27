package iam

import "errors"

// ErrSelfChange is returned, with no request sent, when a guarded write's
// target is the caller itself: for example, a service account token
// resetting its own secret. It wins over ErrPrivilegedChange when both
// would apply.
var ErrSelfChange = errors.New("iam: refused: target is the caller")

// ErrPrivilegedChange is returned, with no request sent, when a guarded
// write's target is a protected principal: one that holds, or would gain,
// an IAM write right. See the design's guard rules for exactly which writes
// this covers.
var ErrPrivilegedChange = errors.New("iam: refused: target is protected")
