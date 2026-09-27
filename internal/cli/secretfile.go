package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// secretFileMode is the mode every --secret-file is created at: it holds a
// secret no one but the owner may read, per the CLI design's secret files
// contract.
const secretFileMode = 0o600

// secretFileFlagName is the flag every secret-file command registers
// through Op.extraFlags, and the name every guard and call below reads back
// from cmd.
const secretFileFlagName = "secret-file"

// registerSecretFileFlag adds --secret-file to cmd, for an Op.extraFlags
// hook: the flag carries no Input field of its own, since the path it names
// is never part of the request body.
func registerSecretFileFlag(cmd *cobra.Command) {
	cmd.Flags().String(secretFileFlagName, "", "path to write the new secret to, mode 0600 (required)")
}

// checkSecretFilePath refuses a --secret-file value the CLI design's secret
// files contract already rules out, before any request: empty, or naming a
// path something already occupies. os.Lstat, not os.Stat, is used
// deliberately so a symlink is refused by its own existing directory entry,
// never followed to whatever it points at (or does not); writeSecretFile
// closes the remaining race, between this check and the actual open, with
// its own O_EXCL and, where the platform supports it, O_NOFOLLOW.
func checkSecretFilePath(path string) error {
	if path == "" {
		return newUsageError("--%s is required", secretFileFlagName)
	}
	if _, err := os.Lstat(path); err == nil {
		return newUsageError("--%s %q already exists; refusing to overwrite it", secretFileFlagName, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return newUsageError("--%s %q: %s", secretFileFlagName, path, err)
	}
	return nil
}

// guardSecretFilePath is a Guard (op.go) that runs checkSecretFilePath on
// cmd's --secret-file value, before any request: every secret-file op uses
// this as its guard, since the check does not depend on the op's own Input.
func guardSecretFilePath(cmd *cobra.Command, _ any) error {
	path, err := cmd.Flags().GetString(secretFileFlagName)
	if err != nil {
		return usageError{msg: err.Error()}
	}
	return checkSecretFilePath(path)
}

// writeSecretFile creates path fresh and writes content as its entire body,
// at secretFileMode: openSecretFileExclusive's O_EXCL rejects a path
// something now occupies, and, on a platform that supports it, O_NOFOLLOW
// refuses a symlink, closing the race between checkSecretFilePath's own
// check and this open. Any failure removes whatever the failed open or
// write left behind, so a partial secret file never survives it; the caller
// still owns deleting the resource the secret belonged to.
func writeSecretFile(path string, content []byte) error {
	f, err := openSecretFileExclusive(path, secretFileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// secretFileFailedError reports the CLI design's SecretFileFailed case: a
// create's own request succeeded, but writing --secret-file failed
// afterward, so the secret is lost either way and the CLI deletes the
// resource it belonged to through the SDK.
type secretFileFailedError struct{ msg string }

func (e secretFileFailedError) Error() string { return e.msg }

// newSecretFileWriteFailed reports a --secret-file write failure after a
// successful create, for the case where the cleanup delete of the new
// resource itself succeeded: the resource is gone, so the message may
// describe writeErr freely.
func newSecretFileWriteFailed(resourceKind, resourceID string, writeErr error) error {
	return secretFileFailedError{msg: fmt.Sprintf(
		"could not write --%s: %s; the new %s %s was deleted", secretFileFlagName, writeErr, resourceKind, resourceID)}
}

// newSecretFileCleanupFailed reports a --secret-file write failure whose
// cleanup delete also failed: per the CLI design, the message names the
// resource only by its kind and ID, never writeErr's or the delete's own
// error text, so it can never repeat whatever either one failed with.
func newSecretFileCleanupFailed(resourceKind, resourceID string) error {
	return secretFileFailedError{msg: fmt.Sprintf(
		"%s %s exists and could not be deleted; delete it manually", resourceKind, resourceID)}
}
