package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/database"
)

var databaseOps = []Op[database.Client]{
	Read[database.Client, database.ListDatabaseEngineVersionsInput, database.ListDatabaseEngineVersionsOutput](kebab("ListDatabaseEngineVersions"), (*database.Client).ListDatabaseEngineVersions),
	Read[database.Client, database.ListDatabaseZonesInput, database.ListDatabaseZonesOutput](kebab("ListDatabaseZones"), (*database.Client).ListDatabaseZones),
	Read[database.Client, database.ListDatabaseFlavorsInput, database.ListDatabaseFlavorsOutput](kebab("ListDatabaseFlavors"), (*database.Client).ListDatabaseFlavors),
	Read[database.Client, database.ListDatabaseStorageTypesInput, database.ListDatabaseStorageTypesOutput](kebab("ListDatabaseStorageTypes"), (*database.Client).ListDatabaseStorageTypes),
	Read[database.Client, database.ListDatabaseNetworksInput, database.ListDatabaseNetworksOutput](kebab("ListDatabaseNetworks"), (*database.Client).ListDatabaseNetworks),
	Read[database.Client, database.ListDatabaseInstancesInput, database.ListDatabaseInstancesOutput](kebab("ListDatabaseInstances"), (*database.Client).ListDatabaseInstances),
	Read[database.Client, database.GetDatabaseInstanceInput, database.GetDatabaseInstanceOutput](kebab("GetDatabaseInstance"), (*database.Client).GetDatabaseInstance),
}

func newDatabaseCmd(e *env) *cobra.Command {
	return Service(e, "database", "Relational database inventory and catalogs", database.New, databaseOps...)
}
