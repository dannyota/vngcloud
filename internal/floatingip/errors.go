// Package floatingip holds private floating IP quote and exposure rules.
package floatingip

import "errors"

// ErrOpenLogin means the rule snapshot cannot establish restricted login access.
var ErrOpenLogin = errors.New("floating IP: login exposure refused")
