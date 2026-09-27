package cli

import (
	"context"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/loadbalancer"
)

// The four file flags import-certificate registers beyond
// ImportCertificateInput's own fields: PEM text is multi-line and awkward in
// argv, and a private key must never reach argv at all, where ps and shell
// history keep it. Name and Type still get their own mechanical --name and
// --type flags; Certificate and CertificateChain are NoFlag'd (see
// importCertificateOp), so only these files, or --cli-input-json, can set
// them; PrivateKey and Passphrase are vngcloud.Secret, so flagSpecsFor
// already never gives them a flag of their own and applyCLIInputJSON
// already refuses them from --cli-input-json entirely.
const (
	certificateFileFlagName      = "certificate-file"
	certificateChainFileFlagName = "certificate-chain-file"
	privateKeyFileFlagName       = "private-key-file"
	passphraseFileFlagName       = "passphrase-file"
)

func registerImportCertificateFileFlags(cmd *cobra.Command) {
	cmd.Flags().String(certificateFileFlagName, "", "path to a PEM certificate file (required)")
	cmd.Flags().String(certificateChainFileFlagName, "", "path to a PEM certificate chain file, leaf-first")
	cmd.Flags().String(privateKeyFileFlagName, "", "path to a PEM private key file")
	cmd.Flags().String(passphraseFileFlagName, "", "path to a file holding the private key's passphrase")
}

// populateImportCertificateInput is import-certificate's Guard: it runs on
// the merged Input, after --cli-input-json and every ordinary flag are
// applied but before checkRequiredFlags and before any request, and reads
// every file flag the operator actually gave into the matching Input field.
// A file flag the operator did not give is left alone, so a value
// --cli-input-json already set for Certificate or CertificateChain
// survives; PrivateKey and Passphrase can never arrive that way (see
// applyCLIInputJSON). --certificate-file wins over any --cli-input-json
// Certificate when both are given, the same way an ordinary flag always
// wins over --cli-input-json for its own field. Certificate,
// CertificateChain, and PrivateKey are set exactly as their file holds
// them; --passphrase-file additionally drops one trailing newline, the rule
// configure set <key> - applies to stdin. Reading these files never depends
// on read-only: they are local files, not a request, and runOp checks
// read-only right after this guard returns, before checkRequiredFlags or
// any call.
func populateImportCertificateInput(cmd *cobra.Command, in any) error {
	input, ok := in.(*loadbalancer.ImportCertificateInput)
	if !ok {
		return nil
	}

	if cmd.Flags().Changed(certificateFileFlagName) {
		path, _ := cmd.Flags().GetString(certificateFileFlagName)
		content, err := readInputFile(certificateFileFlagName, path)
		if err != nil {
			return err
		}
		input.Certificate = content
	}
	if input.Certificate == "" {
		return newUsageError("--%s is required (or set Certificate through --cli-input-json)", certificateFileFlagName)
	}

	if cmd.Flags().Changed(certificateChainFileFlagName) {
		path, _ := cmd.Flags().GetString(certificateChainFileFlagName)
		content, err := readInputFile(certificateChainFileFlagName, path)
		if err != nil {
			return err
		}
		input.CertificateChain = content
	}
	if cmd.Flags().Changed(privateKeyFileFlagName) {
		path, _ := cmd.Flags().GetString(privateKeyFileFlagName)
		content, err := readInputFile(privateKeyFileFlagName, path)
		if err != nil {
			return err
		}
		input.PrivateKey = vngcloud.Secret(content)
	}
	if cmd.Flags().Changed(passphraseFileFlagName) {
		path, _ := cmd.Flags().GetString(passphraseFileFlagName)
		content, err := readInputFile(passphraseFileFlagName, path)
		if err != nil {
			return err
		}
		input.Passphrase = vngcloud.Secret(dropTrailingNewline(content))
	}
	return nil
}

// importCertificateOp builds import-certificate's Op directly, rather than
// through Write, since Certificate and CertificateChain need NoFlag, which
// no writeOption exposes, and the certificate, chain, key, and passphrase
// all arrive through populateImportCertificateInput's file flags instead of
// flags.go's own reflection. methodName is set by hand to
// "ImportCertificate" so checkOpName's name-matches-the-SDK-method rule
// still holds, the same as compute's createSSHKeyOp.
func importCertificateOp() Op[loadbalancer.Client] {
	return Op[loadbalancer.Client]{
		name:       kebab("ImportCertificate"),
		methodName: "ImportCertificate",
		kind:       kindWrite,
		noFlag:     map[string]bool{"Certificate": true, "CertificateChain": true},
		guard:      populateImportCertificateInput,
		extraFlags: registerImportCertificateFileFlags,
		newInput:   func() any { return new(loadbalancer.ImportCertificateInput) },
		newOutput:  func() any { return new(loadbalancer.ImportCertificateOutput) },
		call: func(_ *cobra.Command, client *loadbalancer.Client, ctx context.Context, in any) (any, error) {
			return client.ImportCertificate(ctx, in.(*loadbalancer.ImportCertificateInput))
		},
	}
}
