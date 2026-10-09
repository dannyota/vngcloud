package volume

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// isVolumeError reports whether status is the volume ERROR status, from the
// design's status table.
func isVolumeError(status string) bool {
	return strings.EqualFold(status, "ERROR")
}

// isVolumeDeleted reports whether status is the volume DELETED status: a
// delete wait settles on either this or a 404 from GetVolume.
func isVolumeDeleted(status string) bool {
	return strings.EqualFold(status, "DELETED")
}

// sleepFunc waits for d or ctx's end, whichever comes first, returning
// ctx.Err() when ctx ends first. Tests inject a fake one so the waits below
// never really elapse.
type sleepFunc func(ctx context.Context, d time.Duration) error

// clockFunc reads the current time. poll uses it, alongside a sleepFunc, to
// bound a wait by elapsed wall time rather than by counting poll intervals,
// so a slow read itself counts against the bound. Tests inject a fake
// clock; production uses time.Now.
type clockFunc func() time.Time

// contextSleep is the real sleepFunc.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// poll runs step at once, then again every volumePollInterval, until step
// reports stop true or volumeWaitBound has elapsed, by now, since poll's
// first call to step. Every volume wait in the design's wait table uses
// this same 2-second interval and 5-minute bound, so neither is a
// parameter here, unlike vDNS's, network's, and compute's own unexported
// poll, whose shape this otherwise copies.
func poll(ctx context.Context, now clockFunc, sleep sleepFunc, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(volumeWaitBound)
	for {
		stop, err := step(ctx)
		if stop {
			return err
		}
		if !now().Before(deadline) {
			return onTimeout()
		}
		if err := sleep(ctx, volumePollInterval); err != nil {
			return err
		}
	}
}

// Poll timing per the design's wait table.
const (
	volumePollInterval = 2 * time.Second
	volumeWaitBound    = 5 * time.Minute
)

// waitVolumeAttached is AttachVolume's post-attach wait unless NoWait is
// set: it reads volumeID with GetVolume until its Status is IN-USE with
// serverID among its attached servers (settled) or ERROR (failed); any
// other outcome, including IN-USE without serverID yet listed, keeps it
// polling.
func (c *Client) waitVolumeAttached(ctx context.Context, op, volumeID, serverID string) (*Volume, error) {
	var vol *Volume
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: volumeID})
			if err != nil {
				return true, err
			}
			vol = &out.Volume
			switch {
			case vol.IsInUse() && vol.AttachedToServer(serverID):
				return true, nil
			case isVolumeError(vol.Status):
				return true, fmt.Errorf("%w: %s: volume %s is ERROR", ErrFailed, op, volumeID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: volume %s did not confirm attach to server %s within %s; run this operation again to check",
				ErrNotSettled, op, volumeID, serverID, volumeWaitBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: volume %s: %w", ErrNotSettled, op, volumeID, err)
	}
	return vol, err
}

// waitVolumeDetached is DetachVolume's post-detach wait unless NoWait is
// set: it reads volumeID with GetVolume until its Status reaches AVAILABLE
// or ERROR; any other status keeps it polling.
func (c *Client) waitVolumeDetached(ctx context.Context, op, volumeID string) (*Volume, error) {
	var vol *Volume
	err := poll(ctx, c.now, c.sleep,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: volumeID})
			if err != nil {
				return true, err
			}
			vol = &out.Volume
			switch {
			case vol.IsAvailable():
				return true, nil
			case isVolumeError(vol.Status):
				return true, fmt.Errorf("%w: %s: volume %s is ERROR", ErrFailed, op, volumeID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: volume %s did not confirm detach within %s; run this operation again to check",
				ErrNotSettled, op, volumeID, volumeWaitBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: volume %s: %w", ErrNotSettled, op, volumeID, err)
	}
	return vol, err
}
