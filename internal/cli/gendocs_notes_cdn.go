package cli

// cdn's doc notes: docServiceIntro (gendocs_notes_iam.go) keys
// cdnServiceIntroNote by "cdn", and docOpNotes (gendocs_notes.go) keys the
// notes below by "cdn <op-name>".

const cdnServiceIntroNote = "`list-ip-ranges` needs no credential. The other commands call the vCDN API " +
	"with a vCDN API key, which is separate from the IAM login. Only the root user can make the key, in the " +
	"vCDN Portal, with an expiry of up to one year. Store it with `vngcloud configure set vcdn_api_key -` " +
	"(the key on stdin) or set `VNGCLOUD_VCDN_API_KEY`; there is no flag for it. Without a key a command " +
	"exits 3 with error code `NoCredentials` and sends nothing.\n\n" +
	"Any key can read every key and certificate of the account, so keep its expiry short. The SDK sends no " +
	"`Origin` header, so a key's `AllowOriginHeader` does not apply. A 401 (error code `Unauthorized`, exit " +
	"3) means the key is wrong or expired."

const cdnListCertificatesNote = "Lists every certificate of the account without its PEM text. The server " +
	"also returns each private key; the CLI never prints it."

const cdnGetCertificateNote = "Prints the certificate and CA chain as PEM text. The server also returns the " +
	"private key; the CLI never prints it."

const cdnListAPIKeysNote = "`Current` marks the key this command sent, so its `ExpiresAt` is the expiry of " +
	"the key in use. The server also returns every key's token and the account email; the CLI never prints " +
	"them."

const cdnListWebAcceleratorsNote = "Lists portal-created Web Accelerators. `CDNDomain` is the generated CNAME target."

const cdnAnalyticsNote = "Uses generated CDN domains, not customer domains. Analytics calls use POST but remain reads, so read-only allows them."

const cdnUpdateWebAcceleratorNote = "Merges changes into a fresh CDN read. Use --cli-input-json for every list. --no-wait makes one follow-up read and skips the settle wait."

const cdnToggleWebAcceleratorNote = "Returns WebAccelerator and Changed. It confirms with reads at 0, 2, 4, and 8 seconds. --no-wait skips only the settle wait. A CDN that does not settle prints its last read on stdout and exits 1."

const cdnPurgePathsNote = "Purges exact cached paths. Supply Paths through --cli-input-json. Wait at least 30 seconds before another purge of the same CDN. Each purge consumes the package's daily purge limit; the Basic package permits five per day."

// docOpNotesCDN holds cdn's entries of docOpNotes, merged into it at init.
var docOpNotesCDN = map[string]string{
	"cdn list-certificates":       cdnListCertificatesNote,
	"cdn get-certificate":         cdnGetCertificateNote,
	"cdn list-api-keys":           cdnListAPIKeysNote,
	"cdn list-web-accelerators":   cdnListWebAcceleratorsNote,
	"cdn get-traffic":             cdnAnalyticsNote,
	"cdn get-request-rate":        cdnAnalyticsNote,
	"cdn get-cache-status":        cdnAnalyticsNote,
	"cdn get-http-codes":          cdnAnalyticsNote,
	"cdn get-traffic-report":      cdnAnalyticsNote,
	"cdn update-web-accelerator":  cdnUpdateWebAcceleratorNote,
	"cdn enable-web-accelerator":  cdnToggleWebAcceleratorNote,
	"cdn disable-web-accelerator": cdnToggleWebAcceleratorNote,
	"cdn delete-web-accelerator":  "Deletes the CDN and its generated CNAME target. Pass --yes to confirm.",
	"cdn purge-paths":             cdnPurgePathsNote,
}

func init() {
	docJSONPlaceholders["Paths"] = `["/<path>"]`
	for k, v := range docOpNotesCDN {
		docOpNotes[k] = v
	}
}
