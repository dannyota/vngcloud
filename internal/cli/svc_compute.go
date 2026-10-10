package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/pricing"
)

// secretFileCleanupDeleteTimeout bounds the cleanup delete callCreateSSHKey
// sends after a failed --secret-file write: the delete runs on a context
// detached from the command's own (via context.WithoutCancel), so a canceled
// command still cleans up the orphaned key, but this bound keeps that cleanup
// from hanging forever if the network stalls.
const secretFileCleanupDeleteTimeout = 10 * time.Second

// computeOps is compute's operation table. get-ssh-key, import-ssh-key, and
// delete-ssh-key follow the usual Read and Write pattern; create-ssh-key is
// built by hand below, since it needs --secret-file, a flag no Input field
// derives, and adds a SecretFile field to its own Output beyond compute's
// CreateSSHKeyOutput.
//
// ListFlavorZones and ListFlavors are Read, the reads a caller needs before
// pricing or creating a server. QuoteCreateServer is Read too, per ADR 0002
// rule 1: it shares CreateServerInput with the create this design has not
// added yet, but UserData, MaxPrice, and NoWait govern only that future
// create, never this price-only call, so NoFlag hides all three here; every
// other CreateServerInput field, SecurityGroupIDs (a repeatable
// --security-group-id) included, gets its usual flag.
var computeOps = []Op[compute.Client]{
	serverConsoleLogOp(),
	Read[compute.Client, compute.ListServersInput, compute.ListServersOutput](
		kebab("ListServers"), (*compute.Client).ListServers),
	Read[compute.Client, compute.GetServerInput, compute.GetServerOutput](
		kebab("GetServer"), (*compute.Client).GetServer),
	Read[compute.Client, compute.ListSSHKeysInput, compute.ListSSHKeysOutput](
		kebab("ListSSHKeys"), (*compute.Client).ListSSHKeys),
	Read[compute.Client, compute.GetSSHKeyInput, compute.GetSSHKeyOutput](
		kebab("GetSSHKey"), (*compute.Client).GetSSHKey),
	Write[compute.Client, compute.ImportSSHKeyInput, compute.ImportSSHKeyOutput](
		kebab("ImportSSHKey"), (*compute.Client).ImportSSHKey),
	createSSHKeyOp(),
	Write[compute.Client, compute.DeleteSSHKeyInput, compute.DeleteSSHKeyOutput](
		kebab("DeleteSSHKey"), (*compute.Client).DeleteSSHKey, Destructive()),
	Read[compute.Client, compute.ListServerGroupsInput, compute.ListServerGroupsOutput](
		kebab("ListServerGroups"), (*compute.Client).ListServerGroups),
	Read[compute.Client, compute.GetServerGroupInput, compute.GetServerGroupOutput](
		kebab("GetServerGroup"), (*compute.Client).GetServerGroup),
	Write[compute.Client, compute.CreateServerGroupInput, compute.CreateServerGroupOutput](
		kebab("CreateServerGroup"), (*compute.Client).CreateServerGroup),
	Write[compute.Client, compute.UpdateServerGroupInput, compute.UpdateServerGroupOutput](
		kebab("UpdateServerGroup"), (*compute.Client).UpdateServerGroup),
	Write[compute.Client, compute.DeleteServerGroupInput, compute.DeleteServerGroupOutput](
		kebab("DeleteServerGroup"), (*compute.Client).DeleteServerGroup, Destructive()),
	Read[compute.Client, compute.ListServerSecurityGroupsInput, compute.ListServerSecurityGroupsOutput](
		kebab("ListServerSecurityGroups"), (*compute.Client).ListServerSecurityGroups),
	Read[compute.Client, compute.ListServerGroupMembersInput, compute.ListServerGroupMembersOutput](
		kebab("ListServerGroupMembers"), (*compute.Client).ListServerGroupMembers),
	Read[compute.Client, compute.ListServerGroupPoliciesInput, compute.ListServerGroupPoliciesOutput](
		kebab("ListServerGroupPolicies"), (*compute.Client).ListServerGroupPolicies),
	Read[compute.Client, compute.ListOSImagesInput, compute.ListOSImagesOutput](
		kebab("ListOSImages"), (*compute.Client).ListOSImages),
	Read[compute.Client, compute.ListGPUImagesInput, compute.ListGPUImagesOutput](
		kebab("ListGPUImages"), (*compute.Client).ListGPUImages),
	Read[compute.Client, compute.ListUserImagesInput, compute.ListUserImagesOutput](
		kebab("ListUserImages"), (*compute.Client).ListUserImages),
	Read[compute.Client, compute.ListFlavorZonesInput, compute.ListFlavorZonesOutput](
		kebab("ListFlavorZones"), (*compute.Client).ListFlavorZones),
	Read[compute.Client, compute.ListFlavorsInput, compute.ListFlavorsOutput](
		kebab("ListFlavors"), (*compute.Client).ListFlavors),
	Read[compute.Client, compute.CreateServerInput, pricing.GetQuoteOutput](
		kebab("QuoteCreateServer"), (*compute.Client).QuoteCreateServer,
		NoFlag("UserData", "MaxPrice", "NoWait"),
		Optional("Name", "VPCID", "SubnetID", "SecurityGroupIDs", "SSHKeyID")),
	createServerOp(),
	Write[compute.Client, compute.DeleteServerInput, compute.DeleteServerOutput](
		kebab("DeleteServer"), (*compute.Client).DeleteServer, Destructive()),
	Write[compute.Client, compute.StartServerInput, compute.StartServerOutput](
		kebab("StartServer"), (*compute.Client).StartServer),
	Write[compute.Client, compute.StopServerInput, compute.StopServerOutput](
		kebab("StopServer"), (*compute.Client).StopServer, Destructive()),
	Write[compute.Client, compute.RebootServerInput, compute.RebootServerOutput](
		kebab("RebootServer"), (*compute.Client).RebootServer, Destructive()),
	Write[compute.Client, compute.RenameServerInput, compute.RenameServerOutput](
		kebab("RenameServer"), (*compute.Client).RenameServer),
	Read[compute.Client, compute.ResizeServerInput, pricing.GetQuoteOutput](
		kebab("QuoteResizeServer"), (*compute.Client).QuoteResizeServer,
		NoFlag("MaxPrice", "NoWait")),
	// ResizeServer is Destructive although it reads first like every other
	// server toggle: per the paid writes design, it needs --yes because it
	// restarts the server and, unlike start-server or stop-server, can also
	// charge more.
	Write[compute.Client, compute.ResizeServerInput, compute.ResizeServerOutput](
		kebab("ResizeServer"), (*compute.Client).ResizeServer, Destructive()),
}

func newComputeCmd(e *env) *cobra.Command {
	cmd := Service(e, "compute", "Servers, images, and SSH keys", compute.New, computeOps...)
	for _, child := range cmd.Commands() {
		switch child.Name() {
		case "create-server", "quote-create-server", "import-ssh-key":
			child.Long = docOpNotes["compute "+child.Name()]
		}
	}
	return cmd
}

// createSSHKeyOutput is create-ssh-key's own JSON shape: compute's
// CreateSSHKeyOutput, whose PrivateKey already prints "[redacted]" through
// vngcloud.Secret's own encoding, plus the path --secret-file wrote the key
// to. The embedding flattens SSHKey and PrivateKey to the top level, the
// same shape CreateSSHKeyOutput's own fields would have on their own.
type createSSHKeyOutput struct {
	compute.CreateSSHKeyOutput
	SecretFile string
}

// createSSHKeyOp builds create-ssh-key's Op directly, rather than through
// Write, for two reasons Write's options do not cover: --secret-file has no
// backing Input field, and a successful call's own Output type
// (createSSHKeyOutput above) differs from compute.CreateSSHKeyOutput, the
// type Write's generic method parameter would otherwise fix it to.
// methodName is still set to "CreateSSHKey" by hand, so checkOpName's
// name-matches-the-SDK-method rule holds exactly as it does for every op
// Write and Read build from a real method value: the op does call
// compute.Client.CreateSSHKey, just with CLI-side work wrapped around it.
func createSSHKeyOp() Op[compute.Client] {
	return Op[compute.Client]{
		name:       kebab("CreateSSHKey"),
		methodName: "CreateSSHKey",
		kind:       kindWrite,
		guard:      guardSecretFilePath,
		extraFlags: registerSecretFileFlag,
		newInput:   func() any { return new(compute.CreateSSHKeyInput) },
		newOutput:  func() any { return new(createSSHKeyOutput) },
		call:       callCreateSSHKey,
	}
}

// callCreateSSHKey runs the real create, then writes the private key to
// --secret-file, which guardSecretFilePath already checked before this call
// ever ran. Per the CLI design, the file gets the key as returned plus one
// trailing newline, added only when Reveal() does not already end with one.
// A write failure deletes the new key through the SDK, since the key is
// unusable either way once its one-time private key is lost, and reports
// SecretFileFailed; if that delete itself fails, the resource is named only
// by its ID, per the CLI design's secret files contract, never by the write
// or delete error's own text. The cleanup delete runs on a context detached
// from ctx (context.WithoutCancel, with its own short timeout), so a
// canceled command still cleans up the orphaned key; a NotFound from that
// delete counts as cleanup succeeding, since the key is gone either way.
func callCreateSSHKey(cmd *cobra.Command, client *compute.Client, ctx context.Context, in any) (any, error) {
	out, err := client.CreateSSHKey(ctx, in.(*compute.CreateSSHKeyInput))
	if err != nil {
		return out, err
	}
	// guardSecretFilePath ran before any request and already required this
	// flag and checked its path, so the only new failure possible here is
	// the actual write.
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	key := out.PrivateKey.Reveal()
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}
	if writeErr := writeSecretFile(path, []byte(key)); writeErr != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretFileCleanupDeleteTimeout)
		defer cancel()
		if _, delErr := client.DeleteSSHKey(cleanupCtx, &compute.DeleteSSHKeyInput{SSHKeyID: out.SSHKey.ID}); delErr != nil && !vngcloud.IsNotFound(delErr) {
			return nil, newSecretFileCleanupFailed("ssh key", out.SSHKey.ID)
		}
		return nil, newSecretFileWriteFailed("ssh key", out.SSHKey.ID, writeErr)
	}
	return &createSSHKeyOutput{CreateSSHKeyOutput: *out, SecretFile: path}, nil
}
