package volume

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// AttachVolumeInput identifies the volume and the server to attach it to.
type AttachVolumeInput struct {
	VolumeID string `vngcloud:"required"`
	ServerID string `vngcloud:"required"`

	NoWait bool
}

// AttachVolumeOutput is the volume after the call, and whether the call
// itself changed anything. Changed is false only when the volume was
// already attached to ServerID.
type AttachVolumeOutput struct {
	Volume  Volume
	Changed bool
}

// AttachVolume attaches a volume to a server. It reads the volume first:
// already attached to Input.ServerID returns at once with Changed false,
// sending nothing. Attached elsewhere, the PUT reaches the server, which
// refuses it with its own error.
//
// The PUT keeps the transport's normal retries: a repeat is refused as
// already attached, never a second charge.
//
// Without NoWait, AttachVolume then waits up to 5 minutes, polling
// GetVolume every 2 seconds, for the volume to read IN-USE with ServerID
// among its attached servers. ERROR wraps ErrFailed; the bound running
// out, or a read or a sleep failing, wraps ErrNotSettled; run AttachVolume
// again to check, since it always reads first. NoWait returns at once
// instead, with Output.Volume holding the pre-attach read.
func (c *Client) AttachVolume(ctx context.Context, in *AttachVolumeInput) (*AttachVolumeOutput, error) {
	const op = "volume.AttachVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VolumeID", in.VolumeID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerID", in.ServerID); err != nil {
		return nil, err
	}

	current, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: in.VolumeID})
	if err != nil {
		return nil, err
	}
	if current.Volume.AttachedToServer(in.ServerID) {
		return &AttachVolumeOutput{Volume: current.Volume, Changed: false}, nil
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID, "servers", in.ServerID, "attach"}, nil),
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &AttachVolumeOutput{Volume: current.Volume, Changed: true}, nil
	}
	settled, waitErr := c.waitVolumeAttached(ctx, op, in.VolumeID, in.ServerID)
	if settled == nil {
		settled = &current.Volume
	}
	return &AttachVolumeOutput{Volume: *settled, Changed: true}, waitErr
}

// DetachVolumeInput identifies the volume and the server to detach it
// from. AllowRunning must be set to detach from a server that is ACTIVE,
// since the volume may be mounted there and detaching an in-use filesystem
// can lose unwritten data; stop the server first, or unmount and pass
// AllowRunning.
type DetachVolumeInput struct {
	VolumeID string `vngcloud:"required"`
	ServerID string `vngcloud:"required"`

	AllowRunning bool
	NoWait       bool
}

// DetachVolumeOutput is the volume after the call, and whether the call
// itself changed anything. Changed is false only when the volume was not
// attached to ServerID.
type DetachVolumeOutput struct {
	Volume  Volume
	Changed bool
}

// DetachVolume detaches a volume from a server. It reads the volume first:
// not attached to Input.ServerID returns at once with Changed false,
// sending nothing. Attached, but Volume.Bootable (the server's boot
// volume), refuses with ErrBootVolume, sending nothing: detaching the disk
// a server boots from is never allowed here.
//
// Unless AllowRunning is set, DetachVolume then reads the server's own
// Status; ACTIVE refuses with ErrServerRunning, sending nothing, since the
// volume may be mounted there. AllowRunning skips that read entirely,
// since it is the caller's own consent.
//
// The PUT keeps the transport's normal retries: a repeat is refused as
// already available, never a second charge.
//
// Without NoWait, DetachVolume then waits up to 5 minutes, polling
// GetVolume every 2 seconds, for the volume to read AVAILABLE. ERROR wraps
// ErrFailed; the bound running out, or a read or a sleep failing, wraps
// ErrNotSettled; run DetachVolume again to check, since it always reads
// first. NoWait returns at once instead, with Output.Volume holding the
// pre-detach read.
func (c *Client) DetachVolume(ctx context.Context, in *DetachVolumeInput) (*DetachVolumeOutput, error) {
	const op = "volume.DetachVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VolumeID", in.VolumeID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerID", in.ServerID); err != nil {
		return nil, err
	}

	current, err := c.GetVolume(ctx, &GetVolumeInput{VolumeID: in.VolumeID})
	if err != nil {
		return nil, err
	}
	if !current.Volume.AttachedToServer(in.ServerID) {
		return &DetachVolumeOutput{Volume: current.Volume, Changed: false}, nil
	}
	if current.Volume.Bootable {
		return nil, fmt.Errorf("%w: %s: volume %s is server %s's boot volume", ErrBootVolume, op, in.VolumeID, in.ServerID)
	}
	if !in.AllowRunning {
		status, err := c.readServerStatus(ctx, op, in.ServerID)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(status, "ACTIVE") {
			return nil, fmt.Errorf("%w: %s: server %s is ACTIVE; stop it first, or unmount and pass AllowRunning", ErrServerRunning, op, in.ServerID)
		}
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID, "servers", in.ServerID, "detach"}, nil),
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &DetachVolumeOutput{Volume: current.Volume, Changed: true}, nil
	}
	settled, waitErr := c.waitVolumeDetached(ctx, op, in.VolumeID)
	if settled == nil {
		settled = &current.Volume
	}
	return &DetachVolumeOutput{Volume: *settled, Changed: true}, waitErr
}

// readServerStatus reads serverID's own Status field from the vServer
// gateway's server endpoint, the same route compute.GetServer uses,
// without importing the compute package: compute already imports volume
// for DeleteServer's own reads, and an import back would cycle. Every
// other field of the response is left undecoded, since DetachVolume's
// guard needs only Status.
func (c *Client) readServerStatus(ctx context.Context, op, serverID string) (string, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.volumeURL("v2", []string{projectID, "servers", serverID}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return "", err
	}
	return resp.Data.Status, nil
}
