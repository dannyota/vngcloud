package database

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

const deleteOp = "database.DeleteDatabaseInstance"

type deleteBody struct {
	DatabaseInstances []deleteDetail `json:"databaseInstances"`
	Action            string         `json:"action"`
	ResType           string         `json:"resType"`
}

type deleteDetail struct {
	InstancesID string       `json:"instancesId"`
	Config      deleteConfig `json:"config"`
}

type deleteConfig struct {
	CreateFinalBackup bool `json:"createFinalBackup"`
	DeleteAllBackup   bool `json:"deleteAllBackup"`
}

func refuse(why string) error { return fmt.Errorf("%w: %s", ErrDeleteRefused, why) }

func (c *Client) deletePreflight(ctx context.Context, id string) error {
	got, err := c.GetDatabaseInstance(ctx, &GetDatabaseInstanceInput{ID: id})
	if err != nil {
		return err
	}
	inst := got.Instance
	switch {
	case !strings.EqualFold(inst.DatastoreType, engineType):
		return refuse("the instance is not MySQL")
	case !strings.EqualFold(inst.Status, "ACTIVE"):
		return refuse("the instance is not in the stable ACTIVE state")
	case inst.Owner != nil && !*inst.Owner:
		return refuse("the instance is shared, not owned by this account")
	case inst.Deleted != nil && *inst.Deleted:
		return refuse("the instance is already marked deleted")
	case len(inst.Replicas) > 0 || inst.ReplicaSourceID != "":
		return refuse("the instance has replication")
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	if inst.ProjectID == "" || inst.ProjectID != projectID {
		return refuse("the instance is not in the selected project")
	}
	zones, err := c.ListDatabaseZones(ctx, nil)
	if err != nil {
		return err
	}
	if inst.ZoneID == "" || !hasMatch(zones.Items, func(z Zone) bool { return z.UUID == inst.ZoneID || z.Name == inst.ZoneID }) {
		return refuse("the instance zone is not a database zone of this region")
	}
	var backups []struct {
		ID string `json:"id"`
	}
	if err := c.read(ctx, deleteOp, []string{"backups", "insId", id}, nil, &backups); err != nil {
		return err
	}
	if len(backups) > 0 {
		return refuse("the instance has backups")
	}
	return nil
}

// DeleteDatabaseInstance deletes one backup-free, replica-free MySQL instance
// after an exact-ID preflight, sending the delete once and waiting for
// confirmed absence. Deleted is true only after that confirmation.
func (c *Client) DeleteDatabaseInstance(ctx context.Context, in *DeleteDatabaseInstanceInput) (*DeleteDatabaseInstanceOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: %s: input is required", core.ErrInvalidInput, deleteOp)
	}
	if err := core.CheckRequired(deleteOp, in); err != nil {
		return nil, err
	}
	if !validID(in.ID) {
		return nil, fmt.Errorf("%w: %s: ID must contain only letters, digits, hyphens, and underscores", core.ErrInvalidInput, deleteOp)
	}
	if err := c.writeProvenance(); err != nil {
		return nil, err
	}
	if err := c.deletePreflight(ctx, in.ID); err != nil {
		return nil, err
	}
	out := &DeleteDatabaseInstanceOutput{ID: in.ID}
	body := deleteBody{DatabaseInstances: []deleteDetail{{InstancesID: in.ID}}, Action: "delete", ResType: "dbaas"}
	var raw json.RawMessage
	var credentials []string
	notSent := false
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: deleteOp, Method: http.MethodPost,
		URL:  c.c.RouteURL(routes.Route{Product: routes.ProductDatabase, Version: "vdb-relational/v1", Parts: []string{"database-instances", in.ID, "delete"}}),
		Body: body, OK: okStatuses2xx(), AllowEmptyBody: true, Once: true, NoRedirect: true, Sensitive: true, KeepObservedStatus: true,
		MaxBody: 1 << 20, NotSent: &notSent, SentCredentials: &credentials,
	}, &raw)
	if err != nil {
		if definitiveWriteError(err, notSent, status) {
			return nil, writeError(deleteOp, err, nil)
		}
		return out, fmt.Errorf("%w: delete request unconfirmed", ErrNotSettled)
	}
	switch deleteReply(raw, credentials) {
	case replyRejected:
		return nil, &core.APIError{Operation: deleteOp, StatusCode: 200, Code: "DeleteRejected", Message: "delete not accepted"}
	case replyUnsafe:
		return out, fmt.Errorf("%w: delete response withheld", ErrNotSettled)
	}
	err = c.poll(ctx, func(ctx context.Context) (bool, error) {
		got, err := c.GetDatabaseInstance(ctx, &GetDatabaseInstanceInput{ID: in.ID})
		if core.IsNotFound(err) {
			out.Deleted = true
			return true, nil
		}
		if err != nil {
			return true, fmt.Errorf("%w: delete confirmation interrupted", ErrNotSettled)
		}
		switch strings.ToUpper(got.Instance.Status) {
		case "DELETED":
			out.Deleted = true
			return true, nil
		case "ACTIVE", "DELETING":
			return false, nil
		}
		return true, fmt.Errorf("%w: delete status unrecognized", ErrNotSettled)
	})
	return out, settleWait(err, "delete")
}

const (
	replyOK = iota
	replyRejected
	replyUnsafe
)

func deleteReply(raw []byte, credentials []string) int {
	if leaksDecoded(raw, credentials) {
		return replyUnsafe
	}
	var env struct {
		Data []struct {
			Success *bool `json:"success"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil {
		for _, row := range env.Data {
			if row.Success != nil && !*row.Success {
				return replyRejected
			}
		}
	}
	return replyOK
}

func okStatuses2xx() []int {
	codes := make([]int, 100)
	for i := range codes {
		codes[i] = 200 + i
	}
	return codes
}
