package cli

import (
	"context"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

// storageOps is storage's operation table. Region is a vStorage region name
// such as HCM04, not the global --region value, and an empty one maps the
// configured region, so its flag name would only collide with the global
// flag; NoFlag keeps it settable through --cli-input-json. ProjectID is a
// vStorage project ID, which the global --project-id flag supplies.
var storageOps = []Op[storage.Client]{
	Read[storage.Client, storage.GetProjectAutoRenewInput, storage.GetProjectAutoRenewOutput](
		"get-project-auto-renew", (*storage.Client).GetProjectAutoRenew, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Write[storage.Client, storage.PutProjectAutoRenewInput, storage.PutProjectAutoRenewOutput](
		"put-project-auto-renew", (*storage.Client).PutProjectAutoRenew, WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID"),
		Guard(func(_ *cobra.Command, input any) error {
			if input.(*storage.PutProjectAutoRenewInput).Enabled == nil {
				return newUsageError("--enabled=true or --enabled=false is required; JSON Enabled must be non-null")
			}
			return nil
		})),
	getBucketEncryptionOp(),
	putBucketEncryptionOp(),
	Read[storage.Client, storage.ListRegionsInput, storage.ListRegionsOutput](
		kebab("ListRegions"), (*storage.Client).ListRegions),
	Read[storage.Client, storage.ListProjectsInput, storage.ListProjectsOutput](
		kebab("ListProjects"), (*storage.Client).ListProjects, NoFlag("Region")),
	Read[storage.Client, storage.ListProjectTypesInput, storage.ListProjectTypesOutput](
		kebab("ListProjectTypes"), (*storage.Client).ListProjectTypes, NoFlag("Region")),
	Read[storage.Client, storage.CreateProjectInput, storage.QuoteCreateProjectOutput](
		kebab("QuoteCreateProject"), (*storage.Client).QuoteCreateProject,
		NoFlag("Region", "Name", "MaxPrice", "NoWait"), Optional("Name")),
	createStorageProjectOp(),
	Write[storage.Client, storage.DeleteProjectInput, storage.DeleteProjectOutput](
		kebab("DeleteProject"), (*storage.Client).DeleteProject, Destructive(),
		WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Read[storage.Client, storage.ListBucketsInput, storage.ListBucketsOutput](
		kebab("ListBuckets"), (*storage.Client).ListBuckets, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Read[storage.Client, storage.GetBucketInput, storage.GetBucketOutput](
		kebab("GetBucket"), (*storage.Client).GetBucket, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Write[storage.Client, storage.CreateBucketInput, storage.CreateBucketOutput](
		kebab("CreateBucket"), (*storage.Client).CreateBucket, WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.DeleteBucketInput, storage.DeleteBucketOutput](
		kebab("DeleteBucket"), (*storage.Client).DeleteBucket, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Read[storage.Client, storage.ListS3KeysInput, storage.ListS3KeysOutput](
		"list-s3-keys", (*storage.Client).ListS3Keys, NoFlag("Region"), GlobalProjectID("ProjectID")),
	createS3KeyOp(),
	Write[storage.Client, storage.DeleteS3KeyInput, storage.DeleteS3KeyOutput](
		"delete-s3-key", (*storage.Client).DeleteS3Key, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.AttachS3KeyInput, storage.AttachS3KeyOutput](
		"attach-s3-key", (*storage.Client).AttachS3Key, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.DetachS3KeyInput, storage.DetachS3KeyOutput](
		"detach-s3-key", (*storage.Client).DetachS3Key, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.EnsureServiceAccountPrincipalInput, storage.EnsureServiceAccountPrincipalOutput](
		kebab("EnsureServiceAccountPrincipal"), (*storage.Client).EnsureServiceAccountPrincipal,
		WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Read[storage.Client, storage.GetBucketPolicyInput, storage.GetBucketPolicyOutput](
		kebab("GetBucketPolicy"), (*storage.Client).GetBucketPolicy, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Write[storage.Client, storage.PutBucketPolicyInput, storage.PutBucketPolicyOutput](
		kebab("PutBucketPolicy"), (*storage.Client).PutBucketPolicy,
		WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID"), Guard(guardPutBucketPolicy)),
	Write[storage.Client, storage.DeleteBucketPolicyInput, storage.DeleteBucketPolicyOutput](
		kebab("DeleteBucketPolicy"), (*storage.Client).DeleteBucketPolicy,
		WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Read[storage.Client, storage.GetBucketVersioningInput, storage.GetBucketVersioningOutput](
		kebab("GetBucketVersioning"), (*storage.Client).GetBucketVersioning, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Write[storage.Client, storage.PutBucketVersioningInput, storage.PutBucketVersioningOutput](
		kebab("PutBucketVersioning"), (*storage.Client).PutBucketVersioning,
		WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Read[storage.Client, storage.GetBucketCORSInput, storage.GetBucketCORSOutput](
		kebab("GetBucketCORS"), (*storage.Client).GetBucketCORS, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Write[storage.Client, storage.PutBucketCORSInput, storage.PutBucketCORSOutput](
		kebab("PutBucketCORS"), (*storage.Client).PutBucketCORS, WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.DeleteBucketCORSInput, storage.DeleteBucketCORSOutput](
		kebab("DeleteBucketCORS"), (*storage.Client).DeleteBucketCORS, WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
}

// Name is required for purchase, but the SDK input also serves quotes.
type storageCreateProjectInput struct {
	Region   string
	Name     string `vngcloud:"required"`
	Type     string `vngcloud:"required"`
	QuotaGB  int64  `vngcloud:"required"`
	MaxPrice float64
	NoWait   bool
}

func createStorageProjectOp() Op[storage.Client] {
	op := Write[storage.Client, storageCreateProjectInput, storage.CreateProjectOutput](
		"create-project", func(c *storage.Client, ctx context.Context, in *storageCreateProjectInput) (*storage.CreateProjectOutput, error) {
			input := storage.CreateProjectInput(*in)
			return c.CreateProject(ctx, &input)
		}, WriteNoFlag("Region"))
	op.methodName = "CreateProject"
	return op
}

func newStorageCmd(e *env) *cobra.Command {
	cmd := serviceTableViews(e, "storage", "vStorage regions, projects, and buckets", storage.New, storageOps, map[string]func(any) any{
		"get-project-auto-renew": func(out any) any {
			return projectAutoRenewTable(out.(*storage.GetProjectAutoRenewOutput).State)
		},
		"put-project-auto-renew": func(out any) any {
			result := out.(*storage.PutProjectAutoRenewOutput)
			if result == nil {
				return out
			}
			row := projectAutoRenewTable(result.State)
			if row != nil {
				row["Changed"] = result.Changed
			}
			return row
		},
	})
	for _, child := range cmd.Commands() {
		switch child.Name() {
		case "get-bucket-encryption", "put-bucket-encryption", "create-bucket", "get-project-auto-renew", "put-project-auto-renew":
			child.Long = docOpNotesStorage["storage "+child.Name()]
		}
	}
	return cmd
}

func encryptionBucketFlag(cmd *cobra.Command) {
	cmd.Flags().String("bucket", "", "bucket name; JSON field BucketName (required)")
}

func encryptionBucketName(cmd *cobra.Command, name string) string {
	if cmd.Flags().Changed("bucket") {
		name, _ = cmd.Flags().GetString("bucket")
	}
	return name
}

func getBucketEncryptionOp() Op[storage.Client] {
	op := Read[storage.Client, storage.GetBucketEncryptionInput, storage.GetBucketEncryptionOutput](
		"get-bucket-encryption", (*storage.Client).GetBucketEncryption,
		NoFlag("Region", "BucketName"), Optional("BucketName"), GlobalProjectID("ProjectID"))
	op.extraFlags = encryptionBucketFlag
	op.call = func(cmd *cobra.Command, c *storage.Client, ctx context.Context, input any) (any, error) {
		in := input.(*storage.GetBucketEncryptionInput)
		in.BucketName = encryptionBucketName(cmd, in.BucketName)
		return c.GetBucketEncryption(ctx, in)
	}
	return op
}

// A pointer preserves omission and null until the CLI has checked presence.
type storagePutBucketEncryptionInput struct {
	Region     string
	ProjectID  string `vngcloud:"required"`
	BucketName string `vngcloud:"required"`
	Enabled    *bool  `vngcloud:"required"`
}

func putBucketEncryptionOp() Op[storage.Client] {
	op := Write[storage.Client, storagePutBucketEncryptionInput, storage.PutBucketEncryptionOutput](
		"put-bucket-encryption", func(c *storage.Client, ctx context.Context, in *storagePutBucketEncryptionInput) (*storage.PutBucketEncryptionOutput, error) {
			return c.PutBucketEncryption(ctx, &storage.PutBucketEncryptionInput{
				Region: in.Region, ProjectID: in.ProjectID, BucketName: in.BucketName, Enabled: *in.Enabled,
			})
		}, WriteNoFlag("Region", "BucketName"), WriteGlobalProjectID("ProjectID"),
		Guard(func(cmd *cobra.Command, input any) error {
			in := input.(*storagePutBucketEncryptionInput)
			in.BucketName = encryptionBucketName(cmd, in.BucketName)
			if in.Enabled == nil {
				return newUsageError("--enabled=true or --enabled=false is required; JSON Enabled must be non-null")
			}
			return nil
		}))
	op.methodName = "PutBucketEncryption"
	op.extraFlags = encryptionBucketFlag
	return op
}

// Queries keep the SDK output shape. Unfiltered tables use the command's view.
func serviceTableViews[C any](e *env, name, short string, newClient func(vngcloud.Config) *C, ops []Op[C], views map[string]func(any) any) *cobra.Command {
	format := ""
	tableOps := slices.Clone(ops)
	for i := range tableOps {
		view := views[tableOps[i].name]
		if view == nil {
			continue
		}
		call := tableOps[i].call
		tableOps[i].call = func(cmd *cobra.Command, client *C, ctx context.Context, input any) (any, error) {
			out, err := call(cmd, client, ctx, input)
			if format == outputTable && e.flags.query == "" && !isNilOutput(out) {
				out = view(out)
			}
			return out, err
		}
	}
	return Service(e, name, short, func(cfg vngcloud.Config) *C {
		format = resolveOutput(e.flags, cfg)
		return newClient(cfg)
	}, tableOps...)
}

func projectAutoRenewTable(state *storage.ProjectAutoRenew) map[string]any {
	if state == nil {
		return nil
	}
	price := func(value *float64) any {
		if value == nil {
			return "unavailable"
		}
		return *value
	}
	next := price(state.NextCharge)
	if state.Enabled != nil && !*state.Enabled {
		next = "none scheduled"
	}
	return map[string]any{
		"ProjectID": state.ProjectID, "ProjectName": state.ProjectName, "Region": state.Region,
		"EndTime": state.EndTime.UTC().Format(time.RFC3339), "RenewType": state.RenewType,
		"Enabled": state.Enabled, "PeriodMonths": state.PeriodMonths, "QuotePeriodMonths": state.QuotePeriodMonths,
		"QuotedRenewalCharge": price(state.QuotedRenewalCharge), "NextCharge": next,
		"Currency": state.Currency, "PriceStatus": state.PriceStatus, "PriceErrorCode": state.PriceErrorCode,
	}
}
