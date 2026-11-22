// Package database reads relational database inventory and catalogs.
package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// Client shares the configuration's credentials and session.
type Client struct{ c *core.Client }

// New creates a database client.
func New(cfg vngcloud.Config) *Client { return &Client{c: core.ClientOf(cfg)} }

func malformed(op string) error {
	return &core.APIError{Operation: op, StatusCode: 200, Code: "InvalidResponse", Message: "invalid response; body withheld"}
}

func safeError(op string, err error) error {
	e := &core.APIError{Operation: op, Message: "request failed; response withheld"}
	var ae *core.APIError
	if errors.As(err, &ae) {
		e.StatusCode = ae.StatusCode
		e.Retryable = ae.Retryable
	}
	e.Code = core.ResolvedCode(e.StatusCode, "")
	switch {
	case errors.Is(err, context.Canceled):
		e.Err = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		e.Err = context.DeadlineExceeded
	case errors.Is(err, core.ErrAuth):
		e.Err = core.ErrAuth
	case errors.Is(err, core.ErrPermission):
		e.Err = core.ErrPermission
	case errors.Is(err, core.ErrNotFound):
		e.Err = core.ErrNotFound
	case errors.Is(err, core.ErrRateLimited):
		e.Err = core.ErrRateLimited
	}
	return e
}

func (c *Client) read(ctx context.Context, op string, parts []string, q url.Values, out any) error {
	if c.c.Region() != "hcm-3" {
		return fmt.Errorf("%w: database requires hcm-3", core.ErrInvalidConfig)
	}
	var raw json.RawMessage
	var credentials []string
	status, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: "GET", URL: c.c.RouteURL(routes.Route{Product: routes.ProductDatabase, Version: "vdb-relational/v1", Parts: parts, Query: q}), OK: []int{200}, Sensitive: true, WithholdMessage: "database response withheld", SentCredentials: &credentials}, &raw)
	if err != nil {
		if status == 200 {
			return malformed(op)
		}
		return safeError(op, err)
	}
	if !uniqueJSON(raw, credentials) {
		return malformed(op)
	}
	var env struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Code == nil || *env.Code != 200 || len(env.Data) == 0 || bytes.Equal(env.Data, []byte("null")) || json.Unmarshal(env.Data, out) != nil {
		return malformed(op)
	}
	return nil
}

func uniqueJSON(raw []byte, credentials []string) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	if scanValue(d, credentials) != nil {
		return false
	}
	_, err := d.Token()
	return errors.Is(err, io.EOF)
}

func scanValue(d *json.Decoder, credentials []string) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if value, ok := token.(string); ok && slices.Contains(credentials, value) {
		return errors.New("reflected credential")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			tok, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := tok.(string)
			if !ok {
				return errors.New("invalid key")
			}
			if !canonicalKey(key) || slices.Contains(credentials, key) {
				return errors.New("invalid key case")
			}
			key = strings.ToLower(key)
			if seen[key] {
				return errors.New("duplicate key")
			}
			seen[key] = true
			if e = scanValue(d, credentials); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e := scanValue(d, credentials); e != nil {
				return e
			}
		}
	default:
		return errors.New("invalid delimiter")
	}
	_, err = d.Token()
	return err
}

func canonicalKey(key string) bool {
	for _, known := range []string{"code", "data", "id", "name", "status", "type", "version", "versionName", "licenseName", "uuid", "zoneType", "flavorId", "vcpus", "ram", "packageSku", "locateZoneId", "minVolumeSize", "maxVolumeSize", "zoneId", "volumeTypeZoneId", "volumeTypeSku", "networkUuid", "displayName", "subnets", "datastoreType", "datastoreVersion", "projectId", "subnetId", "volumeType", "volumeSize", "quotaPackageId", "publicAccess", "backupAuto", "owner", "role", "replicaSourceId", "replicas", "deleted", "totalPages", "totalElements", "size", "number", "maxSize", "pageObject"} {
		if strings.EqualFold(key, known) {
			return key == known
		}
	}
	return true
}

func validID(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func invalid() error { return fmt.Errorf("%w: invalid database selection", core.ErrInvalidInput) }

// ListDatabaseEngineVersions reads the datastore catalog.
func (c *Client) ListDatabaseEngineVersions(ctx context.Context, _ *ListDatabaseEngineVersionsInput) (*ListDatabaseEngineVersionsOutput, error) {
	var rows []EngineVersion
	if err := c.read(ctx, "ListDatabaseEngineVersions", []string{"database-instances", "datastore"}, nil, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, malformed("ListDatabaseEngineVersions")
	}
	for _, row := range rows {
		if row.Type == "" || row.Version == "" {
			return nil, malformed("ListDatabaseEngineVersions")
		}
	}
	return &ListDatabaseEngineVersionsOutput{Items: rows}, nil
}

// ListDatabaseZones reads the database zones catalog.
func (c *Client) ListDatabaseZones(ctx context.Context, _ *ListDatabaseZonesInput) (*ListDatabaseZonesOutput, error) {
	var rows []Zone
	if err := c.read(ctx, "ListDatabaseZones", []string{"database-instances", "zones"}, nil, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, malformed("ListDatabaseZones")
	}
	for _, row := range rows {
		if !validID(row.UUID) || row.Status == "" {
			return nil, malformed("ListDatabaseZones")
		}
	}
	return &ListDatabaseZonesOutput{Items: rows}, nil
}

// ListDatabaseFlavors reads the flavor catalog for an engine and zone.
func (c *Client) ListDatabaseFlavors(ctx context.Context, in *ListDatabaseFlavorsInput) (*ListDatabaseFlavorsOutput, error) {
	if in == nil || !validID(in.Engine) || in.EngineVersion == "" || !validID(in.ZoneID) {
		return nil, invalid()
	}
	q := url.Values{"type": {in.Engine}, "version": {in.EngineVersion}, "zoneId": {in.ZoneID}}
	var rows []Flavor
	if err := c.read(ctx, "ListDatabaseFlavors", []string{"database-instances", "flavors"}, q, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, malformed("ListDatabaseFlavors")
	}
	for _, row := range rows {
		if row.ID <= 0 {
			return nil, malformed("ListDatabaseFlavors")
		}
	}
	return &ListDatabaseFlavorsOutput{Items: rows}, nil
}

// ListDatabaseStorageTypes reads the volume type catalog for a zone.
func (c *Client) ListDatabaseStorageTypes(ctx context.Context, in *ListDatabaseStorageTypesInput) (*ListDatabaseStorageTypesOutput, error) {
	if in == nil || !validID(in.ZoneID) {
		return nil, invalid()
	}
	var data struct {
		ProjectID string        `json:"projectId"`
		Rows      []StorageType `json:"data"`
	}
	if err := c.read(ctx, "ListDatabaseStorageTypes", []string{"database-instances", "volume", "types"}, url.Values{"zoneId": {in.ZoneID}}, &data); err != nil {
		return nil, err
	}
	if data.Rows == nil {
		return nil, malformed("ListDatabaseStorageTypes")
	}
	for _, row := range data.Rows {
		if row.Type == "" || row.MinVolumeSize <= 0 || row.MaxVolumeSize < row.MinVolumeSize {
			return nil, malformed("ListDatabaseStorageTypes")
		}
	}
	return &ListDatabaseStorageTypesOutput{ProjectID: data.ProjectID, Items: data.Rows}, nil
}

// ListDatabaseNetworks reads the eligible networks for a zone.
func (c *Client) ListDatabaseNetworks(ctx context.Context, in *ListDatabaseNetworksInput) (*ListDatabaseNetworksOutput, error) {
	if in == nil || !validID(in.ZoneID) {
		return nil, invalid()
	}
	var rows []Network
	if err := c.read(ctx, "ListDatabaseNetworks", []string{"database-instances", "networks", "subnets"}, url.Values{"zoneId": {in.ZoneID}}, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, malformed("ListDatabaseNetworks")
	}
	for _, row := range rows {
		if !validID(row.UUID) || row.Subnets == nil {
			return nil, malformed("ListDatabaseNetworks")
		}
		for _, subnet := range row.Subnets {
			if !validID(subnet.UUID) || (subnet.NetworkUUID != "" && subnet.NetworkUUID != row.UUID) {
				return nil, malformed("ListDatabaseNetworks")
			}
		}
	}
	return &ListDatabaseNetworksOutput{Items: rows}, nil
}

type listInstance Instance
type detailInstance Instance

// ListDatabaseInstances reads one page of relational instances.
func (c *Client) ListDatabaseInstances(ctx context.Context, in *ListDatabaseInstancesInput) (*ListDatabaseInstancesOutput, error) {
	page, size := int32(1), int32(100)
	if in != nil {
		if in.PageNumber < 0 || in.PageSize < 0 {
			return nil, invalid()
		}
		if in.PageNumber != 0 {
			page = in.PageNumber
		}
		if in.PageSize != 0 {
			size = in.PageSize
		}
	}
	q := url.Values{"pageNumber": {strconv.Itoa(int(page))}, "pageSize": {strconv.Itoa(int(size))}}
	var data struct {
		ProjectID string         `json:"projectId"`
		Rows      []listInstance `json:"data"`
		Page      *Page          `json:"pageObject"`
	}
	if err := c.read(ctx, "ListDatabaseInstances", []string{"database-instances"}, q, &data); err != nil {
		return nil, err
	}
	if data.Rows == nil || data.Page == nil {
		return nil, malformed("ListDatabaseInstances")
	}
	items := make([]Instance, len(data.Rows))
	for i, row := range data.Rows {
		if !validID(row.ID) || row.Name == "" || row.Status == "" {
			return nil, malformed("ListDatabaseInstances")
		}
		items[i] = Instance(row)
	}
	return &ListDatabaseInstancesOutput{ProjectID: data.ProjectID, Items: items, Page: *data.Page}, nil
}

// GetDatabaseInstance reads one relational instance by its exact ID.
func (c *Client) GetDatabaseInstance(ctx context.Context, in *GetDatabaseInstanceInput) (*GetDatabaseInstanceOutput, error) {
	if in == nil || !validID(in.ID) {
		return nil, invalid()
	}
	var row detailInstance
	if err := c.read(ctx, "GetDatabaseInstance", []string{"database-instances", "id", in.ID}, nil, &row); err != nil {
		return nil, err
	}
	if row.ID != in.ID || row.Name == "" || row.Status == "" {
		return nil, malformed("GetDatabaseInstance")
	}
	return &GetDatabaseInstanceOutput{Instance: Instance(row)}, nil
}
