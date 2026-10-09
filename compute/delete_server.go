package compute

import (
	"context"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/volume"
)

// deleteServerBody is DeleteServer's request body.
type deleteServerBody struct {
	DeleteAllVolume bool `json:"deleteAllVolume"`
}

// DeleteServerInput identifies the server to delete. The boot volume is
// always deleted with the server. With DeleteVolumes false, attached data
// volumes stay and keep being billed; with it true, they are deleted with
// the server, data included.
type DeleteServerInput struct {
	ServerID string `vngcloud:"required"`

	DeleteVolumes bool
	NoWait        bool
}

// DeleteServerOutput names what happened to the server's volumes.
// DeletedVolumeIDs is set only when Input.DeleteVolumes was true and names
// volumes requested for deletion, not confirmed deleted; otherwise KeptVolumeIDs
// names the attached data volumes the API kept, so the caller sees what still
// costs money. The boot volume goes with the server and is never kept, with or
// without NoWait.
type DeleteServerOutput struct {
	DeletedVolumeIDs []string
	KeptVolumeIDs    []string
}

// DeleteServer deletes a server. It reads the server, then lists its
// volumes with volume.ListVolumesByServer, before sending anything: an
// unknown ServerID fails here with core.ErrNotFound rather than reaching
// the delete request at all.
//
// It sends deleteAllVolume equal to Input.DeleteVolumes. The server's own
// refusals (a server that is CREATING, CREATING-BILLING, DELETING, or
// otherwise not deletable) reach the caller as a *core.APIError. DELETE is
// idempotent and keeps the transport's normal retries.
//
// Without NoWait, DeleteServer waits up to 10 minutes, polling GetServer
// every 5 seconds, for a 404 or a read showing Status DELETED. If a read
// instead shows ERROR, or the bound runs out, or a read or a sleep fails,
// such as from a canceled ctx, the returned error wraps ErrFailed or
// ErrNotSettled, and Output still names the volumes listed before the
// delete (DeletedVolumeIDs or KeptVolumeIDs, whichever Input.DeleteVolumes
// selects), unconfirmed, rather than an empty Output the caller would have
// to re-derive; a rerun is safe either way, since DeleteServer always reads
// first.
//
// Once the delete settles, DeleteServer reports the volumes it listed
// before the delete: with Input.DeleteVolumes true, DeletedVolumeIDs names
// all of them, since deleteAllVolume told the server to remove them with
// the server itself; with it false, DeleteServer reads each one again with
// volume.GetVolume, and KeptVolumeIDs names every one whose read did not
// confirm it gone: only a core.ErrNotFound counts a volume as deleted, so a
// transient read failure never hides one that may still be billing. NoWait
// skips both the wait and this reconciliation: DeletedVolumeIDs names every
// volume listed before the delete, and KeptVolumeIDs every one except the
// boot volume, unconfirmed.
func (c *Client) DeleteServer(ctx context.Context, in *DeleteServerInput) (*DeleteServerOutput, error) {
	const op = "compute.DeleteServer"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "ServerID", in.ServerID); err != nil {
		return nil, err
	}

	server, err := c.GetServer(ctx, &GetServerInput{ServerID: in.ServerID})
	if err != nil {
		return nil, err
	}
	volumesBefore, err := c.volume.ListVolumesByServer(ctx, &volume.ListVolumesByServerInput{ServerID: in.ServerID})
	if err != nil {
		return nil, err
	}
	volumeIDs := make([]string, 0, len(volumesBefore.Items))
	for _, v := range volumesBefore.Items {
		// The boot volume is deleted with the server whatever DeleteVolumes
		// says, so it is never a candidate for KeptVolumeIDs.
		if !in.DeleteVolumes && v.UUID == server.Server.BootVolumeID {
			continue
		}
		volumeIDs = append(volumeIDs, v.UUID)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.computeURL("v2", []string{projectID, "servers", in.ServerID}, nil),
		Body:      deleteServerBody{DeleteAllVolume: in.DeleteVolumes},
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		if in.DeleteVolumes {
			return &DeleteServerOutput{DeletedVolumeIDs: volumeIDs}, nil
		}
		return &DeleteServerOutput{KeptVolumeIDs: volumeIDs}, nil
	}
	if err := c.waitServerDeleted(ctx, op, in.ServerID); err != nil {
		// The wait itself failed, but the volumes this server held before the
		// delete still cost money either way; name them with the error rather
		// than an empty Output, so the caller does not have to re-derive them.
		if in.DeleteVolumes {
			return &DeleteServerOutput{DeletedVolumeIDs: volumeIDs}, err
		}
		return &DeleteServerOutput{KeptVolumeIDs: volumeIDs}, err
	}

	if in.DeleteVolumes {
		return &DeleteServerOutput{DeletedVolumeIDs: volumeIDs}, nil
	}
	kept := make([]string, 0, len(volumeIDs))
	for _, id := range volumeIDs {
		// A volume counts as kept unless its read confirms it is gone: any
		// other error, including a network failure, must not hide a
		// resource that may still be billing.
		if _, err := c.volume.GetVolume(ctx, &volume.GetVolumeInput{VolumeID: id}); !core.IsNotFound(err) {
			kept = append(kept, id)
		}
	}
	return &DeleteServerOutput{KeptVolumeIDs: kept}, nil
}
