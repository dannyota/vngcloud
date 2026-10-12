package cli

import "github.com/spf13/cobra"

func storageObjectDocOps() []docOp {
	var ops []docOp
	for _, put := range []bool{true, false} {
		name, kind := "get-object", "Read, local replacement requires --yes"
		if put {
			name, kind = "put-object", "Write, replacement requires --yes"
		}
		fields := extraDocFields(func(cmd *cobra.Command) { registerStorageObjectFlags(cmd, new(storageObjectFlags), put) })
		ops = append(ops, docOp{name: name, kind: kind, fields: fields})
	}
	return ops
}

func storageObjectNotes(put bool) string {
	common := "Require explicit --s3-region HCM04 or HAN02 and --s3-credentials-file with a private [default] AWS-format access-key pair. Ignore management region, project, auth and ambient S3 credentials. Explicit --region and --project-id are refused. Linux and macOS only. No --cli-input-json, stdin, stdout bytes, '-' or file:// paths. --output and --query select metadata only: Bucket, Key, ContentLength, SHA256 and ETag; downloads add Destination. The whole transfer deadline defaults to 5m; --transfer-timeout accepts 1s through 1h. --replace requires --yes; --yes alone preserves the default protection. No automatic retry follows a failed transfer or completed-transfer output failure."
	if put {
		return common + " Upload one completed regular file through required --body, with a 5000000000-byte cap and private snapshot disk use. Default PUT creates only and refuses an existing key. HAN02 create-only PUT is unavailable; HAN02 permits explicit --replace --yes. Read-only refuses upload before credentials or body access. An unconfirmed write requires download and byte-length/SHA256 comparison before retrying. ETag is opaque; SHA256 describes local bytes, not verified remote content."
	}
	return common + " Download to required --destination in a trusted existing directory. Default publication never clobbers an occupied path. --replace --yes atomically replaces a regular local file only. Read-only permits download. --max-download-bytes defaults to 5000000000, accepts positive values through 9223372036854775806, and never permits unlimited downloads. Publish only after clean EOF, sync and close. Published cleanup errors preserve the completed file and name local cleanup. ETag is opaque; SHA256 describes received bytes, not an independent remote checksum."
}

func storageObjectExample(put bool) string {
	base := "vngcloud storage "
	if put {
		return base + "put-object --s3-region HCM04 --s3-credentials-file ./storage.credentials --bucket app-files --key archives/bundle.bin --body ./bundle.bin"
	}
	return base + "get-object --s3-region HCM04 --s3-credentials-file ./storage.credentials --bucket app-files --key archives/bundle.bin --destination ./downloaded.bin"
}
