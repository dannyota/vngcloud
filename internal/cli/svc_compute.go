package cli

import (
	"context"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/compute"
)

// computeOps is compute's operation table. get-ssh-key, import-ssh-key, and
// delete-ssh-key follow the usual Read and Write pattern; create-ssh-key is
// built by hand below, since it needs --secret-file, a flag no Input field
// derives, and adds a SecretFile field to its own Output beyond compute's
// CreateSSHKeyOutput.
var computeOps = []Op[compute.Client]{
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
}

func newComputeCmd(e *env) *cobra.Command {
	return Service(e, "compute", "Servers, images, and SSH keys", compute.New, computeOps...)
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
// ever ran. A write failure deletes the new key through the SDK, since the
// key is unusable either way once its one-time private key is lost, and
// reports SecretFileFailed; if that delete itself fails, the resource is
// named only by its ID, per the CLI design's secret files contract, never
// by the write or delete error's own text.
func callCreateSSHKey(cmd *cobra.Command, client *compute.Client, ctx context.Context, in any) (any, error) {
	out, err := client.CreateSSHKey(ctx, in.(*compute.CreateSSHKeyInput))
	if err != nil {
		return out, err
	}
	// guardSecretFilePath ran before any request and already required this
	// flag and checked its path, so the only new failure possible here is
	// the actual write.
	path, _ := cmd.Flags().GetString(secretFileFlagName)
	if writeErr := writeSecretFile(path, []byte(out.PrivateKey.Reveal())); writeErr != nil {
		if _, delErr := client.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{SSHKeyID: out.SSHKey.ID}); delErr != nil {
			return nil, newSecretFileCleanupFailed("ssh key", out.SSHKey.ID)
		}
		return nil, newSecretFileWriteFailed("ssh key", out.SSHKey.ID, writeErr)
	}
	return &createSSHKeyOutput{CreateSSHKeyOutput: *out, SecretFile: path}, nil
}
