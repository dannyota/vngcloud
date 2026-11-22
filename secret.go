package vngcloud

import "danny.vn/vngcloud/internal/core"

// Secret holds a credential value and redacts ordinary formatting and encoding.
// Use Reveal to access the value. Do not use %p, gob, or reflection to print it.
type Secret = core.Secret
