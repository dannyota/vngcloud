package cli

// computeImportSSHKeyPreferredNote documents the wiki's own recommendation
// for import-ssh-key, and the key type GreenNode actually accepts: the flag
// table cannot show either. Only an RSA public key is accepted live; an
// ed25519 or ECDSA key gets a 400 "Invalid public key" from the server.
const computeImportSSHKeyPreferredNote = "Preferred over create-ssh-key: PublicKey is made elsewhere, for example by\n" +
	"ssh-keygen, so the private key never reaches GreenNode at all. Refuses a\n" +
	"PublicKey that spans more than one line, or that contains the text \"PRIVATE\n" +
	"KEY\", before any request; neither error ever quotes the value. Only RSA public\n" +
	"keys are accepted: ed25519 and ECDSA keys are refused by the server with 400\n" +
	"\"Invalid public key\"."

// computeQuoteCreateServerNote documents quote-create-server's own price
// guard exemptions and unit, which flags are optional, and the gateway's
// ROOT DISK text: the flag table shows every CreateServerInput field the
// same way create-server itself will, with no hint that this command never
// orders anything or that three of those fields do nothing here.
const computeQuoteCreateServerNote = "Never orders anything: prices the server CreateServerInput describes without\n" +
	"sending a create. OptimumPrice and every other price are VND a month, one\n" +
	"prepaid period. Needs only the flags that set the price: --zone-id, --flavor-id,\n" +
	"--image-id, --root-disk-size, --root-disk-type-id, and the data disk pair\n" +
	"--data-disk-size with --data-disk-type-id when there is a data disk. --name,\n" +
	"--vpc-id, --subnet-id, and --security-group-id are optional here and required by\n" +
	"create-server. --ssh-key-id is optional here; create-server needs exactly one of\n" +
	"--ssh-key-id or --user-data-file. The CLI does not send them or any other\n" +
	"unpriced field to the billing gateway, but still checks the shape of each one\n" +
	"that is set, so a bad ID fails here as it will at the create. Refuses\n" +
	"--cli-input-json that sets UserData, since it can hold secrets. Ignores MaxPrice\n" +
	"and NoWait, which govern only an actual create. The gateway's ROOT DISK text\n" +
	"shows the volume type ID where the size belongs; the size is the\n" +
	"--root-disk-size value.\n" +
	"\n" +
	serverEncryptionNote + "\n\n" + idsForCreateServerLink

// computeCreateServerNote documents create-server's own price guard
// default, its duplicate-name guard, --user-data-file, the unretried order,
// and the post-order wait bound: the flag table shows --max-price as a
// plain, optional float and lists no UserData field at all, with no hint of
// any of this.
const computeCreateServerNote = "Orders nothing above --max-price, default 0: a bare create-server refuses with\n" +
	"error code PriceAboveMax until --max-price is raised to at least the quoted\n" +
	"price. A quote of 0 is refused as Unpriced whatever --max-price says. Refuses,\n" +
	"before any quote or order request, a server already named --name exactly. Needs\n" +
	"at least one --security-group-id; the SDK picks no default, so the project's\n" +
	"own default group (open to the world on several ports) is only used when named\n" +
	"explicitly.\n" +
	"Set exactly one of --ssh-key-id or --user-data-file. GreenNode refuses both\n" +
	"together. With user data, the cloud-config must install authorized keys, for\n" +
	"example with ssh_authorized_keys. Cloud-init user data comes only from\n" +
	"--user-data-file <path>, read once at most 64 KiB: it has no plain string flag,\n" +
	"and an inline or file:// --cli-input-json value that sets UserData is refused\n" +
	"outright, since either could put a secret on argv or in a JSON file that shell\n" +
	"history or a process listing keeps; user data never reaches stdout, stderr, or\n" +
	"--debug output. The order itself is never retried after a failure that may have\n" +
	"already reached the server; list servers by name before ordering again rather\n" +
	"than repeating this command. Without --no-wait, waits up to 15 minutes for the\n" +
	"new server to reach ACTIVE, then prints it; a timeout, or ERROR during that\n" +
	"wait, is NotSettled or WriteFailed, and this create must not be repeated.\n" +
	"--no-wait returns at once with only the new server's UUID and Name set.\n" +
	"\n" +
	serverEncryptionNote + "\n\n" + idsForCreateServerLink + "\n\n" + vserverDriftNote
