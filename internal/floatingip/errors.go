// Package floatingip holds private floating IP quote and exposure rules.
package floatingip

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrOpenLogin means the rule snapshot cannot establish restricted login access.
var ErrOpenLogin = errors.New("floating IP: login exposure refused")

var safeProtocolName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,15}$`)

func unsupportedRuleEncoding(protocol string) error {
	reason := fmt.Errorf("%w: unsupported rule encoding", ErrOpenLogin)
	if safeProtocolName.MatchString(protocol) {
		return fmt.Errorf("%w (protocol %q)", reason, protocol)
	}
	return reason
}
