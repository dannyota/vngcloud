package volume

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

func invalidVolumeResponse(op string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "volume identity or state is missing, conflicting, or invalid; body withheld"}
}

func (c *Client) readVolume(ctx context.Context, in *GetVolumeInput) (*GetVolumeOutput, json.RawMessage, error) {
	const op = "volume.GetVolume"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, nil, err
	}
	if err := core.CheckPathID(op, "VolumeID", in.VolumeID); err != nil {
		return nil, nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, nil, err
	}
	var raw json.RawMessage
	if err := c.c.DoJSON(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID}, nil), OK: []int{http.StatusOK}}, &raw); err != nil {
		return nil, nil, err
	}
	var resp struct {
		Data *Volume `json:"data"`
	}
	if jsonresponse.ValidateStrict(raw, &resp) != nil || json.Unmarshal(raw, &resp) != nil || resp.Data == nil || resp.Data.UUID != in.VolumeID {
		return nil, nil, invalidVolumeResponse(op)
	}
	if strings.TrimSpace(resp.Data.Status) == "" {
		return nil, nil, &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "volume status is missing; body withheld", Err: ErrUnexpectedStatus}
	}
	var data struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return nil, nil, invalidVolumeResponse(op)
	}
	return &GetVolumeOutput{Volume: *resp.Data}, data.Data, nil
}

func knownDetachedVolume(raw json.RawMessage) bool {
	var state struct {
		ServerID     json.RawMessage `json:"serverId"`
		ServerIDList *[]string       `json:"serverIdList"`
	}
	var serverID string
	return json.Unmarshal(raw, &state) == nil && len(state.ServerID) > 0 && json.Unmarshal(state.ServerID, &serverID) == nil && serverID == "" && state.ServerIDList != nil && len(*state.ServerIDList) == 0
}
