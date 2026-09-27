package volume

import "errors"

var (
	// ErrNotSettled means a write's post-write wait did not confirm the
	// resource reached its target state before the bound ran out, or a
	// read or a sleep in that wait failed, such as from a canceled ctx. A
	// create must not be repeated once this is returned for it, since the
	// resource exists; every other write that returns it reads first and is
	// safe to run again.
	ErrNotSettled = errors.New("volume: write accepted but not settled")

	// ErrFailed means a wait observed the resource reach ERROR.
	ErrFailed = errors.New("volume: resource reached ERROR")

	// ErrVolumeInUse means a write was refused because a pre-write read
	// showed the volume attached to a server: DeleteVolume when the volume
	// is IN-USE or lists a server, or the server's own refusal for the same
	// reason. In the first case nothing was sent; in the second, the
	// request reached the server.
	ErrVolumeInUse = errors.New("volume: volume in use")
)
