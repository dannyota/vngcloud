package database

import (
	"errors"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

// ErrNotSettled means a write may have succeeded but has not been confirmed.
var ErrNotSettled = core.ErrNotSettled

//nolint:revive
var ErrFailed = core.ErrFailed

// ErrDeleteRefused means a preflight check found the instance ineligible for deletion; nothing was sent.
var ErrDeleteRefused = errors.New("database: delete refused")

// CreateDatabaseInstanceInput selects one private, single-node MySQL 8.0
// instance. The instance has no backups and no public address; creation spends
// credits.
type CreateDatabaseInstanceInput struct {
	// Name is the instance name.
	Name string `vngcloud:"required"`
	// ZoneID is the database zone UUID from list-database-zones.
	ZoneID string `vngcloud:"required"`
	// FlavorID is the flavor ID from list-database-flavors; its monthlyCost is a catalog estimate, not a quote.
	FlavorID int `vngcloud:"required"`
	// StorageTypeID is the storage type token from list-database-storage-types.
	StorageTypeID string `vngcloud:"required"`
	// StorageSize is the storage size in GB, within the storage type bounds.
	StorageSize int `vngcloud:"required"`
	// VPCID is the VPC ID from list-database-networks.
	VPCID string `vngcloud:"required"`
	// SubnetID is a subnet of that VPC from list-database-networks.
	SubnetID string `vngcloud:"required"`
	// DatabaseName is the name of the one logical database to create.
	DatabaseName string `vngcloud:"required"`
	// Username is the master user name.
	Username string `vngcloud:"required"`
	// Password is the master user password. It is sent only in the create request.
	Password vngcloud.Secret
}

// CreateDatabaseInstanceOutput holds the instance as last read; on a partial result only the ID is certain.
type CreateDatabaseInstanceOutput struct{ Instance Instance }

// DeleteDatabaseInstanceInput identifies the instance to delete.
type DeleteDatabaseInstanceInput struct {
	// ID is the exact instance ID.
	ID string `vngcloud:"required"`
}

// DeleteDatabaseInstanceOutput identifies the instance and whether its deletion was confirmed.
type DeleteDatabaseInstanceOutput struct {
	ID      string
	Deleted bool
}
