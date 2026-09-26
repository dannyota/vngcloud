// Package volume lists and reads vServer volumes, volume types, and
// snapshots.
package volume

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client is the volume service client.
type Client struct {
	c *core.Client
}

// New builds a Client from cfg. A Client built from the same Config as
// another service client shares its login and token cache.
func New(cfg vngcloud.Config) *Client {
	return &Client{c: core.ClientOf(cfg)}
}

type ListVolumesInput struct {
	Name string
	Page int
	Size int
}

type ListVolumesOutput = core.PagedList[Volume]

func (c *Client) ListVolumes(ctx context.Context, in *ListVolumesInput) (*ListVolumesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	name := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	if in != nil {
		name = in.Name
		if in.Page > 0 {
			page = in.Page
		}
		if in.Size > 0 {
			size = in.Size
		}
	}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))

	var resp listVolumesResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.ListVolumes",
		Method:    "GET",
		URL:       c.volumeURL("v2", []string{projectID, "volumes"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type GetVolumeInput struct {
	VolumeID string `vngcloud:"required"`
}

type GetVolumeOutput struct {
	Volume Volume
}

func (c *Client) GetVolume(ctx context.Context, in *GetVolumeInput) (*GetVolumeOutput, error) {
	if err := core.CheckRequired("volume.GetVolume", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data Volume `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.GetVolume",
		Method:    "GET",
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetVolumeOutput{Volume: resp.Data}, nil
}

type GetUnderlyingVolumeInput struct {
	VolumeID string `vngcloud:"required"`
}

type GetUnderlyingVolumeOutput struct {
	Volume Volume
}

func (c *Client) GetUnderlyingVolume(ctx context.Context, in *GetUnderlyingVolumeInput) (*GetUnderlyingVolumeOutput, error) {
	if err := core.CheckRequired("volume.GetUnderlyingVolume", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp Volume
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.GetUnderlyingVolume",
		Method:    "GET",
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID, "mapping"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetUnderlyingVolumeOutput{Volume: resp}, nil
}

type ListVolumeTypeZonesInput struct {
	ZoneID string
}

type ListVolumeTypeZonesOutput = core.List[VolumeTypeZone]

func (c *Client) ListVolumeTypeZones(ctx context.Context, in *ListVolumeTypeZonesInput) (*ListVolumeTypeZonesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if in != nil && in.ZoneID != "" {
		q.Set("zoneId", in.ZoneID)
	}

	var resp struct {
		VolumeTypeZones []VolumeTypeZone `json:"volumeTypeZones"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.ListVolumeTypeZones",
		Method:    "GET",
		URL:       c.volumeURL("v1", []string{projectID, "volume_type_zones"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListVolumeTypeZonesOutput{Items: resp.VolumeTypeZones}, nil
}

type ListVolumeTypesInput struct {
	VolumeTypeZoneID string
}

type ListVolumeTypesOutput = core.List[VolumeType]

func (c *Client) ListVolumeTypes(ctx context.Context, in *ListVolumeTypesInput) (*ListVolumeTypesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	parts := []string{projectID, "volume_types"}
	if in != nil && in.VolumeTypeZoneID != "" {
		parts = []string{projectID, in.VolumeTypeZoneID, "volume_types"}
	}

	var resp struct {
		VolumeTypes []VolumeType `json:"volumeTypes"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.ListVolumeTypes",
		Method:    "GET",
		URL:       c.volumeURL("v1", parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &ListVolumeTypesOutput{Items: resp.VolumeTypes}, nil
}

type GetVolumeTypeInput struct {
	VolumeTypeID string `vngcloud:"required"`
}

type GetVolumeTypeOutput struct {
	VolumeType VolumeType
}

func (c *Client) GetVolumeType(ctx context.Context, in *GetVolumeTypeInput) (*GetVolumeTypeOutput, error) {
	if err := core.CheckRequired("volume.GetVolumeType", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		VolumeTypes []VolumeType `json:"volumeTypes"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.GetVolumeType",
		Method:    "GET",
		URL:       c.volumeURL("v1", []string{projectID, "volume_types", in.VolumeTypeID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	if len(resp.VolumeTypes) == 0 {
		return nil, fmt.Errorf("%w: volume type %s", core.ErrNotFound, in.VolumeTypeID)
	}
	return &GetVolumeTypeOutput{VolumeType: resp.VolumeTypes[0]}, nil
}

type GetDefaultVolumeTypeInput struct{}

type GetDefaultVolumeTypeOutput struct {
	VolumeType VolumeType
}

func (c *Client) GetDefaultVolumeType(ctx context.Context, _ *GetDefaultVolumeTypeInput) (*GetDefaultVolumeTypeOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ID     string `json:"volumeTypeId"`
		ZoneID string `json:"volumeTypeZoneId"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.GetDefaultVolumeType",
		Method:    "GET",
		URL:       c.volumeURL("v1", []string{projectID, "volume_default_id"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	if resp.ID == "" {
		return nil, fmt.Errorf("%w: default volume type", core.ErrNotFound)
	}
	return &GetDefaultVolumeTypeOutput{VolumeType: VolumeType{
		ID: resp.ID, VolumeTypeID: resp.ID, ZoneID: resp.ZoneID, VolumeTypeZoneID: resp.ZoneID,
	}}, nil
}

type ListEncryptionTypesInput struct{}

type ListEncryptionTypesOutput = core.List[EncryptionType]

func (c *Client) ListEncryptionTypes(ctx context.Context, _ *ListEncryptionTypesInput) (*ListEncryptionTypesOutput, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.ListEncryptionTypes",
		Method:    "GET",
		URL:       c.volumeURL("v1", []string{projectID, "volumes", "encryption_types"}, nil),
		OK:        []int{200},
	}, &raw); err != nil {
		return nil, err
	}
	var items []EncryptionType
	if err := json.Unmarshal(raw, &items); err == nil {
		return &ListEncryptionTypesOutput{Items: items}, nil
	}
	var resp listEncryptionTypesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return &ListEncryptionTypesOutput{Items: resp.Items}, nil
}

type ListSnapshotsInput struct {
	VolumeID string `vngcloud:"required"`
	Page     int
	Size     int
}

type ListSnapshotsOutput = core.PagedList[Snapshot]

func (c *Client) ListSnapshots(ctx context.Context, in *ListSnapshotsInput) (*ListSnapshotsOutput, error) {
	if err := core.CheckRequired("volume.ListSnapshots", in); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp listSnapshotsResponse
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: "volume.ListSnapshots",
		Method:    "GET",
		URL:       c.volumeURL("v2", []string{projectID, "volumes", in.VolumeID, "snapshots"}, core.PageQuery(in.Page, in.Size)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.Items, resp.Page, resp.PageSize, resp.TotalPages, resp.TotalItems), nil
}

type ListAllSnapshotsInput struct{}

type ListAllSnapshotsOutput = core.List[Snapshot]

func (c *Client) ListAllSnapshots(ctx context.Context, _ *ListAllSnapshotsInput) (*ListAllSnapshotsOutput, error) {
	volumes, err := c.ListVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]Snapshot, 0)
	for _, vol := range volumes.Items {
		snapshots, err := c.ListSnapshots(ctx, &ListSnapshotsInput{VolumeID: vol.UUID})
		if err != nil {
			return nil, err
		}
		items = append(items, snapshots.Items...)
	}
	return &ListAllSnapshotsOutput{Items: items}, nil
}

func (c *Client) volumeURL(version string, parts []string, q url.Values) string {
	return c.c.RouteURL(routes.Route{
		Product: routes.ProductVServer,
		Version: version,
		Parts:   parts,
		Query:   q,
	})
}

type listVolumesResponse struct {
	ListData  []Volume `json:"listData"`
	Page      int      `json:"page"`
	PageSize  int      `json:"pageSize"`
	TotalPage int      `json:"totalPage"`
	TotalItem int      `json:"totalItem"`
}

type listEncryptionTypesResponse struct {
	Items []EncryptionType
}

type listSnapshotsResponse struct {
	Items      []Snapshot `json:"items"`
	Page       int        `json:"page"`
	PageSize   int        `json:"pageSize"`
	TotalPages int        `json:"totalPages"`
	TotalItems int        `json:"totalItems"`
}

func (r *listEncryptionTypesResponse) UnmarshalJSON(data []byte) error {
	var stringsOnly []string
	if err := json.Unmarshal(data, &stringsOnly); err == nil {
		r.Items = encryptionTypesFromStrings(stringsOnly)
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"encryptionTypes", "data", "items"} {
		raw := fields[key]
		if len(raw) == 0 {
			continue
		}
		items, err := decodeEncryptionTypes(raw)
		if err != nil {
			return err
		}
		r.Items = items
		return nil
	}
	r.Items = nil
	return nil
}

func decodeEncryptionTypes(data []byte) ([]EncryptionType, error) {
	var typed []EncryptionType
	if err := json.Unmarshal(data, &typed); err == nil {
		return typed, nil
	}
	var stringsOnly []string
	if err := json.Unmarshal(data, &stringsOnly); err != nil {
		return nil, err
	}
	return encryptionTypesFromStrings(stringsOnly), nil
}

func encryptionTypesFromStrings(values []string) []EncryptionType {
	items := make([]EncryptionType, 0, len(values))
	for _, value := range values {
		items = append(items, EncryptionType{ID: value, Name: value, Value: value})
	}
	return items
}

type Volume struct {
	UUID               string   `json:"uuid"`
	ID                 string   `json:"id"`
	ProjectID          string   `json:"projectId"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	Size               uint64   `json:"size"`
	Status             string   `json:"status"`
	StatusMessage      string   `json:"statusMessage"`
	VolumeTypeID       string   `json:"volumeTypeId"`
	VolumeType         any      `json:"volumeType"`
	VolumeTypeZoneName string   `json:"volumeTypeZoneName"`
	IOPS               string   `json:"iops"`
	Throughput         any      `json:"throughPut"`
	ServerID           string   `json:"serverId"`
	ServerNameList     []string `json:"serverNameList"`
	CreatedAt          string   `json:"createdAt"`
	UpdatedAt          *string  `json:"updatedAt"`
	Bootable           bool     `json:"bootable"`
	EncryptionType     *string  `json:"encryptionType"`
	EncryptionKeyID    string   `json:"encryptionKeyId"`
	BootIndex          int      `json:"bootIndex"`
	MultiAttach        bool     `json:"multiAttach"`
	ServerIDList       []string `json:"serverIdList"`
	Location           *string  `json:"location"`
	Product            string   `json:"product"`
	PersistentVolume   bool     `json:"persistentVolume"`
	MigrateState       string   `json:"migrateState"`
	MaxSize            int      `json:"maxSize"`
	MinSize            int      `json:"minSize"`
	ZoneID             string   `json:"zoneId"`
	Zone               Zone     `json:"zone"`
}

type Zone struct {
	UUID          string   `json:"uuid"`
	Name          string   `json:"name,omitempty"`
	ZoneType      string   `json:"zoneType"`
	IsDefault     bool     `json:"isDefault"`
	Description   string   `json:"description"`
	IsEnabled     bool     `json:"isEnabled"`
	OpenStackZone string   `json:"openstackZone"`
	IPRanges      []string `json:"ipRanges"`
	ServerCount   int      `json:"serverCount"`
	VolumeCount   int      `json:"volumeCount"`
}

type VolumeType struct {
	ID               string `json:"id"`
	VolumeTypeID     string `json:"volumeTypeId"`
	Name             string `json:"name"`
	IOPS             int    `json:"iops"`
	MaxSize          int    `json:"maxSize"`
	MinSize          int    `json:"minSize"`
	Throughput       int    `json:"throughPut"`
	ZoneID           string `json:"zoneId"`
	VolumeTypeZoneID string `json:"volumeTypeZoneId"`
}

// VolumeTypeZone holds only the fields a live ListVolumeTypeZones item
// carries.
type VolumeTypeZone struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Zone        Zone   `json:"zone"`
}

type EncryptionType struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Value       string `json:"value,omitempty"`
	DisplayKey  string `json:"displayKey,omitempty"`
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
}

type Snapshot struct {
	ID               string `json:"id"`
	UUID             string `json:"uuid"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
	DeletedAt        string `json:"deletedAt"`
	VolumeID         string `json:"volumeId"`
	SnapshotVolumeID string `json:"snapshotVolumeId"`
	VolumeTypeID     string `json:"volumeTypeId"`
	VolumeTypeZoneID string `json:"volumeTypeZoneId"`
	ProjectID        string `json:"projectId"`
	UserID           any    `json:"userId"`
	Size             int64  `json:"size"`
	VolumeSize       int64  `json:"volumeSize"`
	Status           string `json:"status"`
	Type             string `json:"type"`
	Product          string `json:"product"`
	BackendID        string `json:"backendId"`
	BackendPool      string `json:"backendPool"`
	BackendPrefix    string `json:"backendPrefix"`
	BackendStatus    string `json:"backendStatus"`
	BackendUUID      string `json:"backendUuid"`
	BootIndex        int    `json:"bootIndex"`
	Bootable         bool   `json:"bootable"`
	EncryptionType   any    `json:"encryptionType"`
	IsPermanently    bool   `json:"isPermanently"`
	MultiAttach      bool   `json:"multiAttach"`
	ParentID         string `json:"parentId"`
	ParentType       string `json:"parentType"`
	PolicySnapshot   any    `json:"policySnapshot"`
	RetainedDays     int    `json:"retainedDays"`
	ScheduleType     string `json:"scheduleType"`
	SnapshotConfig   any    `json:"snapshotConfig"`
	SnapshotTime     any    `json:"snapshotTime"`
	VolumeSnapshot   any    `json:"volumeSnapshot"`
}

func (v Volume) AttachedToServer(serverID string) bool {
	if v.ServerID == serverID {
		return true
	}
	for _, attached := range v.ServerIDList {
		if attached == serverID {
			return true
		}
	}
	return false
}

func (v Volume) IsAvailable() bool {
	return strings.EqualFold(v.Status, "AVAILABLE")
}

func (v Volume) IsInUse() bool {
	return strings.EqualFold(v.Status, "IN-USE")
}

func (v Volume) CanDelete() bool {
	if strings.EqualFold(v.Status, "ERROR") {
		return true
	}
	return v.IsAvailable() && v.ServerID == "" && len(v.ServerIDList) == 0
}
