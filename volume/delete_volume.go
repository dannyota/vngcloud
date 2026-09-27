package volume

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// DeleteVolumeInput identifies the volume to delete. Delete destroys the
// volume's data; there is no undo.
type DeleteVolumeInput struct {
	VolumeID string `vngcloud:"required"`

	NoWait bool
}

type DeleteVolumeOutput struct{}

// DeleteVolume deletes a volume. It reads the volume first and refuses,
// sending nothing, with ErrVolumeInUse when that read shows it IN-USE or
// naming a server: an attached volume carries a workload, and must be
// detached first. The server's own in-use refusal is the final guard for a
// race this pre-check misses.
//
// DELETE is idempotent and keeps the transport's normal retries: a retried
// delete that finds the volume already gone comes back as core.ErrNotFound.
//
// Without NoWait, DeleteVolume then waits up to 5 minutes, polling
// GetVolume every 2 seconds, for a 404 or a read showing Status DELETED. If
// a read instead shows ERROR, or the bound runs out, or a read or a sleep
// fails, such as from a canceled ctx, the returned error wraps ErrFailed or
// ErrNotSettled; a rerun is safe either way, since DeleteVolume always reads
// first.
func (c *Client) DeleteVolume(ctx context.Context, in *DeleteVolumeInput) (*DeleteVolumeOutput, error) {
	const op = "volume.DeleteVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VolumeID", in.VolumeID); err != nil {
		return nil, err
	}

	current, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: in.VolumeID})
	if err != nil {
		return nil, err
	}
	if current.Volume.IsInUse() || current.Volume.ServerID != "" || len(current.Volume.ServerIDList) > 0 {
		return nil, fmt.Errorf("%w: %s: volume %s is attached to a server; detach it first", ErrVolumeInUse, op, in.VolumeID)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID}, nil),
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	if in.NoWait {
		return &DeleteVolumeOutput{}, nil
	}
	if err := c.waitVolumeDeleted(ctx, op, in.VolumeID); err != nil {
		return &DeleteVolumeOutput{}, err
	}
	return &DeleteVolumeOutput{}, nil
}

// waitVolumeDeleted is DeleteVolume's post-delete wait unless NoWait is set:
// it reads volumeID with GetVolume until that read reports NotFound or
// Status DELETED (settled), or ERROR (failed); any other status keeps it
// polling.
func (c *Client) waitVolumeDeleted(ctx context.Context, op, volumeID string) error {
	err := poll(ctx, c.now, c.sleep, volumePollInterval, volumeDeleteBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: volumeID})
			if err != nil {
				if core.IsNotFound(err) {
					return true, nil
				}
				return true, err
			}
			switch {
			case isVolumeDeleted(out.Volume.Status):
				return true, nil
			case isVolumeError(out.Volume.Status):
				return true, fmt.Errorf("%w: %s: volume %s is ERROR", ErrFailed, op, volumeID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: volume %s did not reach 404 or DELETED within %s; delete was sent and a rerun is safe",
				ErrNotSettled, op, volumeID, volumeDeleteBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: volume %s: %w", ErrNotSettled, op, volumeID, err)
	}
	return err
}
