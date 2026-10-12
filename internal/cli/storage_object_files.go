package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/storage"
)

const maxS3CredentialsSize = 16 * 1024

type objectFileResult struct {
	ContentLength int64
	SHA256        string
	Published     bool
}

// Dependencies belong to one call so concurrent transfers cannot change hooks.
type objectFileOps struct {
	supported bool
	open      func(string, bool) (*os.File, error)
	private   func(*os.File) bool
	stat      func(*os.File) (os.FileInfo, error)
	lstat     func(string) (os.FileInfo, error)
	mkdirTemp func(string, string) (string, error)
	create    func(string, string) (*os.File, error)
	copy      func(context.Context, io.Writer, io.Reader, int64) (int64, error)
	sync      func(*os.File) error
	close     func(*os.File) error
	seek      func(*os.File) error
	link      func(string, string) error
	rename    func(string, string) error
	remove    func(string) error
	syncDir   func(string) error
}

func defaultObjectFileOps() objectFileOps {
	return objectFileOps{
		supported: objectFilesSupported(), open: openObjectFile, private: privateObjectCredentials,
		stat: (*os.File).Stat, lstat: os.Lstat, mkdirTemp: os.MkdirTemp, create: os.CreateTemp,
		copy: copyObjectFile, sync: (*os.File).Sync, close: (*os.File).Close,
		seek: func(f *os.File) error { _, err := f.Seek(0, io.SeekStart); return err },
		link: os.Link, rename: os.Rename, remove: os.Remove, syncDir: syncObjectDirectory,
	}
}

func credentialsFileError() error {
	return fmt.Errorf("%w: S3 credentials file is missing, unsafe, or malformed", vngcloud.ErrCredentialsFile)
}

func readS3CredentialsFile(path string) (storage.ObjectCredentials, error) {
	return readS3CredentialsFileWithOps(path, defaultObjectFileOps())
}

func readS3CredentialsFileWithOps(path string, ops objectFileOps) (storage.ObjectCredentials, error) {
	var empty storage.ObjectCredentials
	if !ops.supported {
		return empty, credentialsFileError()
	}
	f, err := ops.open(path, true)
	if err != nil {
		return empty, credentialsFileError()
	}
	defer func() { _ = f.Close() }()
	info, err := ops.stat(f)
	if err != nil || !info.Mode().IsRegular() || !ops.private(f) {
		return empty, credentialsFileError()
	}
	data, err := io.ReadAll(io.LimitReader(f, maxS3CredentialsSize+1))
	if err != nil {
		return empty, credentialsFileError()
	}
	if err := ops.close(f); err != nil {
		return empty, credentialsFileError()
	}
	return parseS3Credentials(data)
}

func parseS3Credentials(data []byte) (storage.ObjectCredentials, error) {
	var empty storage.ObjectCredentials
	if len(data) > maxS3CredentialsSize || !utf8.Valid(data) {
		return empty, credentialsFileError()
	}
	section := false
	values := make(map[string]string, 2)
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		line = strings.Trim(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if line == "[default]" && !section {
			section = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.Trim(key, " \t")
		value = strings.Trim(value, " \t")
		if !section || !ok || (key != "aws_access_key_id" && key != "aws_secret_access_key") || values[key] != "" || !validS3CredentialValue(value) {
			return empty, credentialsFileError()
		}
		values[key] = value
	}
	if len(values) != 2 {
		return empty, credentialsFileError()
	}
	return storage.ObjectCredentials{AccessKeyID: vngcloud.Secret(values["aws_access_key_id"]), SecretAccessKey: vngcloud.Secret(values["aws_secret_access_key"])}, nil
}

func validS3CredentialValue(value string) bool {
	if value == "" || strings.ContainsAny(value, "\"'#$;`") || strings.Contains(value, "%(") {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return false
		}
	}
	return true
}

// Raw reader and filesystem errors may contain object bytes or credentials.
func safeObjectFileError(message string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w", message, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", message, context.DeadlineExceeded)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%s: %w", message, io.ErrUnexpectedEOF)
	default:
		return errors.New(message)
	}
}

func snapshotObjectFile(ctx context.Context, path string) (*os.File, func() error, error) {
	return snapshotObjectFileWithOps(ctx, path, storage.MaxPutObjectSize, defaultObjectFileOps())
}

func snapshotObjectFileWithOps(ctx context.Context, path string, limit int64, ops objectFileOps) (file *os.File, cleanupSnapshot func() error, retErr error) {
	fail := func(err error) (*os.File, func() error, error) {
		return nil, nil, safeObjectFileError("could not snapshot object file", err)
	}
	if !ops.supported {
		return fail(nil)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if path == "-" || strings.HasPrefix(path, "file://") {
		return fail(nil)
	}
	source, err := ops.open(path, false)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = source.Close() }()
	info, err := ops.stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return fail(err)
	}
	dir, err := ops.mkdirTemp("", "vngcloud-object-*")
	if err != nil {
		return fail(err)
	}
	snapshot, err := ops.create(dir, "snapshot-*")
	if err != nil {
		if removeErr := ops.remove(dir); removeErr != nil {
			_, _, snapshotErr := fail(err)
			return nil, nil, errors.Join(snapshotErr, safeObjectFileError("could not clean private object snapshot", removeErr))
		}
		return fail(err)
	}
	cleanup := func() error {
		closeErr := ops.close(snapshot)
		if closeErr != nil {
			_ = snapshot.Close()
		}
		removeErr := ops.remove(snapshot.Name())
		dirErr := ops.remove(dir)
		if closeErr != nil || removeErr != nil || dirErr != nil {
			return safeObjectFileError("could not clean private object snapshot", errors.Join(closeErr, removeErr, dirErr))
		}
		return nil
	}
	complete := false
	defer func() {
		if !complete {
			if cleanupErr := cleanup(); cleanupErr != nil {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	n, err := ops.copy(ctx, snapshot, source, limit)
	if err != nil || n > limit {
		return fail(err)
	}
	if err := ops.close(source); err != nil {
		return fail(err)
	}
	if err := ops.sync(snapshot); err != nil {
		return fail(err)
	}
	if err := ops.seek(snapshot); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	complete = true
	return snapshot, cleanup, nil
}

// A fixed buffer avoids ReaderFrom and WriterTo paths that bypass cancellation.
func copyObjectFile(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit >= 0 {
		src = io.LimitReader(src, limit+1)
	}
	buf := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if n > 0 {
			written, err := dst.Write(buf[:n])
			total += int64(written)
			if err != nil {
				return total, err
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}

func checkObjectDestination(path string, replace bool) error {
	return checkObjectDestinationWithOps(path, replace, defaultObjectFileOps())
}

func checkObjectDestinationWithOps(path string, replace bool, ops objectFileOps) error {
	if !ops.supported {
		return errors.New("object file transfers are unsupported on this platform")
	}
	if path == "" || path == "-" || strings.HasPrefix(path, "file://") {
		return errors.New("object destination must name a file")
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		return errors.New("object destination parent must be an existing directory")
	}
	info, err := ops.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("could not check object destination")
	}
	if !replace || !info.Mode().IsRegular() {
		return errors.New("object destination is occupied or is not a regular file")
	}
	return nil
}

func downloadObjectFile(ctx context.Context, body io.ReadCloser, destination string, replace bool) (objectFileResult, error) {
	return downloadObjectFileWithOps(ctx, body, destination, replace, defaultObjectFileOps())
}

func downloadObjectFileWithOps(ctx context.Context, body io.ReadCloser, destination string, replace bool, ops objectFileOps) (objectFileResult, error) {
	var result objectFileResult
	if body == nil {
		return result, errors.New("object download body is required")
	}
	bodyClosed := false
	defer func() {
		if !bodyClosed {
			_ = body.Close()
		}
	}()
	fail := func(err error) (objectFileResult, error) {
		return result, safeObjectFileError("could not complete object download", err)
	}
	if err := checkObjectDestinationWithOps(destination, replace, ops); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	dir := filepath.Dir(destination)
	f, err := ops.create(dir, ".vngcloud-object-*")
	if err != nil {
		return fail(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
		if !result.Published {
			_ = ops.remove(f.Name())
		}
	}()
	digest := sha256.New()
	n, err := ops.copy(ctx, io.MultiWriter(f, digest), body, -1)
	if err != nil {
		return fail(err)
	}
	bodyClosed = true
	if err := body.Close(); err != nil {
		return fail(err)
	}
	if err := ops.sync(f); err != nil {
		return fail(err)
	}
	if err := ops.close(f); err != nil {
		return fail(err)
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if replace {
		if err := checkObjectDestinationWithOps(destination, true, ops); err != nil {
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := ops.rename(f.Name(), destination); err != nil {
			return fail(err)
		}
	} else {
		if err := ops.link(f.Name(), destination); err != nil {
			return fail(err)
		}
	}
	result = objectFileResult{ContentLength: n, SHA256: hex.EncodeToString(digest.Sum(nil)), Published: true}
	if !replace {
		if err := ops.remove(f.Name()); err != nil {
			return result, errors.New("download was published; remove the extra .vngcloud-object-* temporary link from the destination directory")
		}
	}
	if err := ops.syncDir(dir); err != nil {
		return result, errors.New("download was published; destination directory sync failed")
	}
	return result, nil
}
