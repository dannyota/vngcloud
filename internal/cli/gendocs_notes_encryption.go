package cli

// Encrypted volumes: doc notes for the encryption flags on volume and
// server creates, kept apart from gendocs_notes.go so it stays under the
// length limit.

// encryptionTypeIDsNote says where an encryption type ID comes from.
const encryptionTypeIDsNote = "The ID comes from volume list-encryption-types: aes-xts-plain64_128 or " +
	"aes-xts-plain64_256. An unknown ID is refused by the server."

// volumeListEncryptionTypesNote documents the IDs create-volume and
// create-server take.
const volumeListEncryptionTypesNote = "Lists the encryption types the project offers, live " +
	"aes-xts-plain64_128 and aes-xts-plain64_256. Pass the ID to volume create-volume " +
	"--encryption-type-id, or to compute create-server --root-disk-encryption-type-id and " +
	"--data-disk-encryption-type-id."

// volumeEncryptedPriceNote says how a volume's encryption is priced.
const volumeEncryptedPriceNote = "--encryption-type-id creates an encrypted volume. " + encryptionTypeIDsNote +
	" An encrypted volume costs the same price as a plain one of the same size and type, live. Read the " +
	"encryption type back with volume get-volume; get-underlying-volume does not carry it."

// volumeEncryptedAttachNote says what is known about attaching an encrypted
// volume.
const volumeEncryptedAttachNote = "An encrypted volume attached live to a server created with encrypted " +
	"disks. Attaching one to any other server is untested, and the server may refuse it."

// volumeReadVerifiedNote replaces the unverified-shape warning on the two
// volume reads that ran against a real encrypted volume.
const volumeReadVerifiedNote = "Verified live: this output shape was read from a real encrypted volume."

// volumeGetVolumeNote adds the encryption type to get-volume's note.
var volumeGetVolumeNote = volumeReadVerifiedNote + "\n\nThis command reads a volume's encryption type " +
	"(EncryptionType); null means the volume is not encrypted."

// volumeGetUnderlyingVolumeNote points at get-volume for the encryption type.
var volumeGetUnderlyingVolumeNote = volumeReadVerifiedNote + "\n\nThe output does not carry the encryption " +
	"type; use volume get-volume to read it."

// serverEncryptionNote documents the server encryption flags.
const serverEncryptionNote = "--root-disk-encryption-type-id and --data-disk-encryption-type-id encrypt " +
	"that disk. " + encryptionTypeIDsNote + " --data-disk-encryption-type-id needs --data-disk-size and " +
	"--data-disk-type-id. Either flag sets encryptionVolume, which the billing gateway prices as a " +
	"surcharge on the flavor, 85,140 VND a month on s2-general-1x2 live, not on disk size."
