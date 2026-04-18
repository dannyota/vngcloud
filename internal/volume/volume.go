package volume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type Service struct {
	client *core.Client
}

func New(client *core.Client) *Service {
	return &Service{client: client}
}

type ListVolumesOptions struct {
	Name string
	Page int
	Size int
}

type ListVolumeTypeZonesOptions struct {
	ZoneID string
}

type ListVolumeTypesOptions struct {
	VolumeTypeZoneID string
}
type ListSnapshotsOptions = core.ListOptions

type ListVolumesResult = core.ListResult[Volume]
type ListSnapshotsResult = core.ListResult[Snapshot]

func (s *Service) ListVolumes(ctx context.Context, opts *ListVolumesOptions) (*ListVolumesResult, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	name := ""
	page, size := core.DefaultPage, core.DefaultPageSize
	if opts != nil {
		name = opts.Name
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.Size > 0 {
			size = opts.Size
		}
	}
	q.Set("name", name)
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))

	var resp listVolumesResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.ListVolumes",
		Method:    "GET",
		URL:       s.volumeURL("v2", []string{projectID, "volumes"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.ListData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

func (s *Service) GetVolume(ctx context.Context, id string) (*Volume, error) {
	if id == "" {
		return nil, errors.New("vngcloud: volume id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data Volume `json:"data"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.GetVolume",
		Method:    "GET",
		URL:       s.volumeURL("v2", []string{projectID, "volumes", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

func (s *Service) GetUnderlyingVolume(ctx context.Context, id string) (*Volume, error) {
	if id == "" {
		return nil, errors.New("vngcloud: volume id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp Volume
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.GetUnderlyingVolume",
		Method:    "GET",
		URL:       s.volumeURL("v2", []string{projectID, "volumes", id, "mapping"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) ListVolumeTypeZones(ctx context.Context, opts *ListVolumeTypeZonesOptions) ([]VolumeTypeZone, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if opts != nil && opts.ZoneID != "" {
		q.Set("zoneId", opts.ZoneID)
	}

	var resp struct {
		VolumeTypeZones []VolumeTypeZone `json:"volumeTypeZones"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.ListVolumeTypeZones",
		Method:    "GET",
		URL:       s.volumeURL("v1", []string{projectID, "volume_type_zones"}, q),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.VolumeTypeZones, nil
}

func (s *Service) ListVolumeTypes(ctx context.Context, opts *ListVolumeTypesOptions) ([]VolumeType, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	parts := []string{projectID, "volume_types"}
	if opts != nil && opts.VolumeTypeZoneID != "" {
		parts = []string{projectID, opts.VolumeTypeZoneID, "volume_types"}
	}

	var resp struct {
		VolumeTypes []VolumeType `json:"volumeTypes"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.ListVolumeTypes",
		Method:    "GET",
		URL:       s.volumeURL("v1", parts, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return resp.VolumeTypes, nil
}

func (s *Service) GetVolumeType(ctx context.Context, id string) (*VolumeType, error) {
	if id == "" {
		return nil, errors.New("vngcloud: volume type id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		VolumeTypes []VolumeType `json:"volumeTypes"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.GetVolumeType",
		Method:    "GET",
		URL:       s.volumeURL("v1", []string{projectID, "volume_types", id}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	if len(resp.VolumeTypes) == 0 {
		return nil, fmt.Errorf("%w: volume type %s", core.ErrNotFound, id)
	}
	return &resp.VolumeTypes[0], nil
}

func (s *Service) GetDefaultVolumeType(ctx context.Context) (*VolumeType, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ID     string `json:"volumeTypeId"`
		ZoneID string `json:"volumeTypeZoneId"`
	}
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.GetDefaultVolumeType",
		Method:    "GET",
		URL:       s.volumeURL("v1", []string{projectID, "volume_default_id"}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	if resp.ID == "" {
		return nil, fmt.Errorf("%w: default volume type", core.ErrNotFound)
	}
	return &VolumeType{ID: resp.ID, VolumeTypeID: resp.ID, ZoneID: resp.ZoneID, VolumeTypeZoneID: resp.ZoneID}, nil
}

func (s *Service) ListEncryptionTypes(ctx context.Context) ([]EncryptionType, error) {
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.ListEncryptionTypes",
		Method:    "GET",
		URL:       s.volumeURL("v1", []string{projectID, "volumes", "encryption_types"}, nil),
		OK:        []int{200},
	}, &raw); err != nil {
		return nil, err
	}
	var items []EncryptionType
	if err := json.Unmarshal(raw, &items); err == nil {
		return items, nil
	}
	var resp listEncryptionTypesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (s *Service) ListSnapshots(ctx context.Context, volumeID string, opts *ListSnapshotsOptions) (*ListSnapshotsResult, error) {
	if volumeID == "" {
		return nil, errors.New("vngcloud: volume id is required")
	}
	projectID, err := s.client.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp listSnapshotsResponse
	if err := s.client.DoJSON(ctx, transport.Request{
		Operation: "Volume.ListSnapshots",
		Method:    "GET",
		URL:       s.volumeURL("v2", []string{projectID, "volumes", volumeID, "snapshots"}, core.ListQuery(opts)),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return core.PageResult(resp.Items, resp.Page, resp.PageSize, resp.TotalPages, resp.TotalItems), nil
}

func (s *Service) ListAllSnapshots(ctx context.Context) ([]Snapshot, error) {
	volumes, err := s.ListVolumes(ctx, nil)
	if err != nil {
		return nil, err
	}
	items := make([]Snapshot, 0)
	for _, volume := range volumes.Items {
		snapshots, err := s.ListSnapshots(ctx, volume.UUID, nil)
		if err != nil {
			return nil, err
		}
		items = append(items, snapshots.Items...)
	}
	return items, nil
}

func (s *Service) volumeURL(version string, parts []string, q url.Values) string {
	return s.client.RouteURL(routes.Route{
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

type VolumeTypeZone struct {
	ID              string   `json:"id"`
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	PoolName        []string `json:"poolName"`
	VolumeTypeZones any      `json:"volumeTypeZones"`
	Extra           any      `json:"extra"`
	Success         bool     `json:"success"`
	ErrorCode       string   `json:"errorCode"`
	ErrorMsg        string   `json:"errorMsg"`
	Zone            Zone     `json:"zone"`
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
