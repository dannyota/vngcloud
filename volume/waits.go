package volume

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func isVolumeError(status string) bool {
	return strings.EqualFold(status, "ERROR")
}

func isVolumeDeleted(status string) bool {
	return strings.EqualFold(status, "DELETED")
}

type sleepFunc func(ctx context.Context, d time.Duration) error

type clockFunc func() time.Time

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

func poll(ctx context.Context, now clockFunc, sleep sleepFunc, step func(ctx context.Context) (stop bool, err error), onTimeout func() error) error {
	deadline := now().Add(volumeWaitBound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return onTimeout()
		}
		waitCtx, cancel := context.WithTimeout(ctx, remaining)
		stop, err := step(waitCtx)
		if ctx.Err() != nil {
			cancel()
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		if !now().Before(deadline) || waitCtx.Err() != nil {
			timeoutErr := errors.Join(onTimeout(), waitCtx.Err())
			cancel()
			return timeoutErr
		}
		if stop || err != nil {
			cancel()
			return err
		}
		err = sleep(waitCtx, min(volumePollInterval, deadline.Sub(now())))
		waitErr := waitCtx.Err()
		cancel()
		if ctx.Err() != nil {
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		if !now().Before(deadline) || waitErr != nil {
			return errors.Join(onTimeout(), waitErr)
		}
		if err != nil {
			return err
		}
	}
}

const (
	volumePollInterval = 2 * time.Second
	volumeWaitBound    = 5 * time.Minute
)

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
