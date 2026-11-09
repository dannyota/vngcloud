package compute

import (
	"errors"

	"danny.vn/vngcloud/internal/floatingip"
)

var (
	// ErrOpenLogin means public create could expose login or its rules are unproven.
	ErrOpenLogin = floatingip.ErrOpenLogin

	// ErrFailed means a wait observed a server reach ERROR.
	ErrFailed = errors.New("compute: resource reached ERROR")

	// ErrUnexpectedStatus means StartServer, StopServer, or RebootServer
	// read a server Status this SDK does not act on for that call, such as
	// a reboot of a STOPPED server. Nothing was sent.
	ErrUnexpectedStatus = errors.New("compute: unexpected status")
)
