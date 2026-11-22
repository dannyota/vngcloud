package cli

import (
	"context"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/database"
)

const databasePasswordUsage = "private file holding the master password, or - for stdin"

func databaseCreateOp() Op[database.Client] {
	return Op[database.Client]{
		name: "create-database-instance", methodName: "CreateDatabaseInstance", kind: kindWrite,
		short:       "Create a private single-node MySQL 8.0 instance with backups off; it spends credits.",
		beforeInput: beforeDatabasePasswordInput,
		guard:       databaseCreateGuard,
		newInput:    func() any { return new(database.CreateDatabaseInstanceInput) },
		newOutput:   func() any { return new(database.CreateDatabaseInstanceOutput) },
		extraFlags: func(cmd *cobra.Command) {
			cmd.Flags().String("password-file", "", databasePasswordUsage)
		},
		call: callDatabaseCreate,
	}
}

func databaseDeleteOp() Op[database.Client] {
	return Write[database.Client, database.DeleteDatabaseInstanceInput, database.DeleteDatabaseInstanceOutput](
		kebab("DeleteDatabaseInstance"), (*database.Client).DeleteDatabaseInstance,
		Destructive(), ConfirmationReason("deletes the instance and its data without a final backup"))
}

func beforeDatabasePasswordInput(cmd *cobra.Command) error {
	path, _ := cmd.Flags().GetString("password-file")
	if path == "" {
		return newUsageError("--password-file is required")
	}
	raw, _ := cmd.Flags().GetString("cli-input-json")
	if path == "-" && len(raw) >= 7 && raw[:7] == "file://" {
		return newUsageError("--password-file - cannot share input with file:// JSON")
	}
	return nil
}

func databaseCreateGuard(cmd *cobra.Command, _ any) error {
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		return newUsageError("create-database-instance requires --yes to confirm charges")
	}
	return nil
}

func callDatabaseCreate(cmd *cobra.Command, client *database.Client, ctx context.Context, input any) (any, error) {
	in := *input.(*database.CreateDatabaseInstanceInput)
	path, _ := cmd.Flags().GetString("password-file")
	password, err := readUserPassword(ctx, path, cmd.InOrStdin(), iamPasswordInputTimeout)
	if err != nil {
		return nil, err
	}
	in.Password = password
	defer func() { in.Password = "" }()
	out, err := client.CreateDatabaseInstance(ctx, &in)
	if out == nil {
		return nil, err
	}
	return out, err
}
