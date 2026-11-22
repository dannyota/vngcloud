package database

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

const (
	createOp     = "database.CreateDatabaseInstance"
	engineType   = "mysql"
	engineVer    = "8.0"
	pollInterval = 10 * time.Second
	pollBound    = 30 * time.Minute
)

var (
	instanceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	identifierPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
)

type createUser struct {
	Name      string         `json:"name"`
	Password  string         `json:"password"`
	Databases []createDBName `json:"databases"`
}

type createDBName struct {
	Name string `json:"name"`
}

type createDatabase struct {
	Name         string `json:"name"`
	CharacterSet string `json:"characterSet"`
	Collate      string `json:"collate"`
}

type createBody struct {
	User             createUser       `json:"user"`
	Databases        []createDatabase `json:"databases"`
	Name             string           `json:"name"`
	VolumeType       string           `json:"volumeType"`
	VolumeSize       int32            `json:"volumeSize"`
	DatastoreType    string           `json:"datastoreType"`
	DatastoreVersion string           `json:"datastoreVersion"`
	NetIDs           []string         `json:"netIds"`
	PublicAccess     bool             `json:"publicAccess"`
	BackupAuto       bool             `json:"backupAuto"`
	PackageID        string           `json:"packageId"`
	POC              bool             `json:"poc"`
	LocateZoneID     string           `json:"locateZoneId"`
}

func badField(field, why string) error {
	return fmt.Errorf("%w: %s: %s %s", core.ErrInvalidInput, createOp, field, why)
}

func validateCreate(in *CreateDatabaseInstanceInput) error {
	if in == nil {
		return badField("input", "is required")
	}
	if err := core.CheckRequired(createOp, in); err != nil {
		return err
	}
	switch {
	case !instanceNamePattern.MatchString(in.Name):
		return badField("Name", "must be 1 to 64 letters, digits, hyphens, or underscores, starting with a letter or digit")
	case !validID(in.ZoneID):
		return badField("ZoneID", "must contain only letters, digits, hyphens, and underscores")
	case in.FlavorID <= 0 || in.FlavorID > math.MaxInt32:
		return badField("FlavorID", "must be positive")
	case in.StorageTypeID == "" || len(in.StorageTypeID) > 128 || !utf8.ValidString(in.StorageTypeID) || strings.ContainsFunc(in.StorageTypeID, unicode.IsControl):
		return badField("StorageTypeID", "must be a storage type token from the catalog")
	case in.StorageSize <= 0 || in.StorageSize > math.MaxInt32:
		return badField("StorageSize", "must be positive")
	case !validID(in.VPCID):
		return badField("VPCID", "must contain only letters, digits, hyphens, and underscores")
	case !validID(in.SubnetID):
		return badField("SubnetID", "must contain only letters, digits, hyphens, and underscores")
	case !identifierPattern.MatchString(in.DatabaseName):
		return badField("DatabaseName", "must be 1 to 64 letters, digits, or underscores, starting with a letter")
	case !identifierPattern.MatchString(in.Username):
		return badField("Username", "must be 1 to 64 letters, digits, or underscores, starting with a letter")
	}
	if !validPassword(in.Password.Reveal()) {
		return badField("Password", "must be 8 to 32 characters of letters, digits, and $ ^ _ < >, starting with a letter and ending with a letter or digit")
	}
	return nil
}

func validPassword(p string) bool {
	if len(p) < 8 || len(p) > 32 {
		return false
	}
	for i := 0; i < len(p); i++ {
		ch := p[i]
		letter := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
		digit := ch >= '0' && ch <= '9'
		switch {
		case i == 0 && !letter, i == len(p)-1 && !letter && !digit:
			return false
		case !letter && !digit && !strings.ContainsRune("$^_<>", rune(ch)):
			return false
		}
	}
	return true
}

func (c *Client) planCreate(ctx context.Context, in *CreateDatabaseInstanceInput) (createBody, error) {
	var body createBody
	versions, err := c.ListDatabaseEngineVersions(ctx, nil)
	if err != nil {
		return body, err
	}
	engine := ""
	for _, v := range versions.Items {
		if strings.EqualFold(v.Type, engineType) && v.Version == engineVer {
			if engine != "" && engine != v.Type {
				return body, badField("engine", "is ambiguous in the catalog")
			}
			engine = v.Type
		}
	}
	if engine == "" {
		return body, badField("engine", "MySQL 8.0 is not offered by the catalog")
	}
	zones, err := c.ListDatabaseZones(ctx, nil)
	if err != nil {
		return body, err
	}
	if !hasMatch(zones.Items, func(z Zone) bool { return z.UUID == in.ZoneID }) {
		return body, badField("ZoneID", "is not a database zone")
	}
	flavors, err := c.ListDatabaseFlavors(ctx, &ListDatabaseFlavorsInput{Engine: engine, EngineVersion: engineVer, ZoneID: in.ZoneID})
	if err != nil {
		return body, err
	}
	if !hasMatch(flavors.Items, func(f Flavor) bool { return int(f.ID) == in.FlavorID }) {
		return body, badField("FlavorID", "is not offered for MySQL 8.0 in the zone")
	}
	storage, err := c.ListDatabaseStorageTypes(ctx, &ListDatabaseStorageTypesInput{ZoneID: in.ZoneID})
	if err != nil {
		return body, err
	}
	var chosen *StorageType
	for i := range storage.Items {
		if storage.Items[i].Type == in.StorageTypeID {
			if chosen != nil {
				return body, badField("StorageTypeID", "is ambiguous in the zone")
			}
			chosen = &storage.Items[i]
		}
	}
	if chosen == nil {
		return body, badField("StorageTypeID", "is not offered in the zone")
	}
	if in.StorageSize < int(chosen.MinVolumeSize) || in.StorageSize > int(chosen.MaxVolumeSize) {
		return body, badField("StorageSize", "is outside the storage type bounds")
	}
	networks, err := c.ListDatabaseNetworks(ctx, &ListDatabaseNetworksInput{ZoneID: in.ZoneID})
	if err != nil {
		return body, err
	}
	if !hasMatch(networks.Items, func(n Network) bool {
		return n.UUID == in.VPCID && hasMatch(n.Subnets, func(s Subnet) bool { return s.UUID == in.SubnetID })
	}) {
		return body, badField("SubnetID", "is not a subnet of VPCID in the zone")
	}
	return createBody{
		User:             createUser{Name: in.Username, Password: in.Password.Reveal(), Databases: []createDBName{{Name: in.DatabaseName}}},
		Databases:        []createDatabase{{Name: in.DatabaseName, CharacterSet: "utf8mb4", Collate: "utf8mb4_general_ci"}},
		Name:             in.Name,
		VolumeType:       in.StorageTypeID,
		VolumeSize:       int32(in.StorageSize), //nolint:gosec // bounded by validateCreate
		DatastoreType:    engine,
		DatastoreVersion: engineVer,
		NetIDs:           []string{in.SubnetID},
		PackageID:        strconv.Itoa(in.FlavorID),
		LocateZoneID:     in.ZoneID,
	}, nil
}

func hasMatch[T any](rows []T, match func(T) bool) bool {
	for _, row := range rows {
		if match(row) {
			return true
		}
	}
	return false
}

// CreateDatabaseInstance sends one create request for a private, single-node
// MySQL 8.0 instance with automatic backup off, then waits for ACTIVE. It never
// resends the request: an unconfirmed result returns ErrNotSettled with the
// instance ID when one is known.
func (c *Client) CreateDatabaseInstance(ctx context.Context, in *CreateDatabaseInstanceInput) (*CreateDatabaseInstanceOutput, error) {
	if err := validateCreate(in); err != nil {
		return nil, err
	}
	if err := c.writeProvenance(); err != nil {
		return nil, err
	}
	body, err := c.planCreate(ctx, in)
	if err != nil {
		return nil, err
	}
	password := in.Password.Reveal()
	var raw json.RawMessage
	var credentials []string
	notSent := false
	status, err := c.c.DoJSONStatus(ctx, transport.Request{
		Operation: createOp, Method: http.MethodPost,
		URL:     c.c.RouteURL(routes.Route{Product: routes.ProductDatabase, Version: "vdb-relational/v1", Parts: []string{"payment", "database-instances"}}),
		Headers: map[string]string{"user-type": "IAM_USER"},
		Body:    body, OK: []int{200}, Once: true, NoRedirect: true, Sensitive: true, KeepObservedStatus: true,
		MaxBody: 1 << 20, NotSent: &notSent, SentCredentials: &credentials, Redact: []string{password},
	}, &raw)
	secrets := append(append([]string(nil), credentials...), password)
	if err != nil {
		if definitiveWriteError(err, notSent, status) {
			return nil, writeError(createOp, err, secrets)
		}
		return nil, fmt.Errorf("%w: create request unconfirmed", ErrNotSettled)
	}
	id, ok := parseCreateReply(raw, secrets)
	if !ok {
		return nil, fmt.Errorf("%w: create response lacks a valid instance ID", ErrNotSettled)
	}
	out := &CreateDatabaseInstanceOutput{Instance: Instance{ID: id}}
	if in.NoWait {
		return out, nil
	}
	err = c.poll(ctx, func(ctx context.Context) (bool, error) {
		got, err := c.GetDatabaseInstance(ctx, &GetDatabaseInstanceInput{ID: id})
		if core.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return true, fmt.Errorf("%w: create confirmation interrupted", ErrNotSettled)
		}
		inst := got.Instance
		if reflects(inst, password) {
			return true, fmt.Errorf("%w: create detail withheld", ErrNotSettled)
		}
		out.Instance = inst
		switch strings.ToUpper(inst.Status) {
		case "ACTIVE":
			if !createMatches(inst, body) {
				return true, fmt.Errorf("%w: create detail does not match the request", ErrNotSettled)
			}
			return true, nil
		case "BUILD", "BUILDING":
			return false, nil
		case "ERROR", "FAILED":
			return true, ErrFailed
		}
		return true, fmt.Errorf("%w: create status unrecognized", ErrNotSettled)
	})
	return out, settleWait(err, "create")
}

func createMatches(inst Instance, body createBody) bool {
	return strings.EqualFold(inst.DatastoreType, body.DatastoreType) && inst.DatastoreVersion == body.DatastoreVersion &&
		inst.QuotaPackageID == body.PackageID && inst.BackupAuto != nil && !*inst.BackupAuto &&
		inst.PublicAccess != nil && !*inst.PublicAccess
}

func parseCreateReply(raw []byte, secrets []string) (string, bool) {
	text := string(raw)
	for _, s := range secrets {
		if s != "" && strings.Contains(text, s) {
			return "", false
		}
	}
	if !uniqueJSON(raw, secrets) {
		return "", false
	}
	var env struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	var rows []struct {
		ResourceID string `json:"resourceId"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Code == nil || *env.Code != 200 || json.Unmarshal(env.Data, &rows) != nil || len(rows) != 1 {
		return "", false
	}
	id := rows[0].ResourceID
	if !validID(id) {
		return "", false
	}
	for _, s := range secrets {
		if id == s {
			return "", false
		}
	}
	return id, true
}
