package tokencache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
)

// ErrUnavailable marks a service-account filesystem failure without retaining
// paths, hashes, or filesystem causes. Login errors pass through unchanged.
var ErrUnavailable = errors.New("tokencache: cache unavailable")

// ServiceAccountKey holds only a digest, never client credentials.
type ServiceAccountKey struct{ digest [sha256.Size]byte }

// NewServiceAccountKey binds the cache entry to the immutable secret snapshot.
func NewServiceAccountKey(profile, clientID, tokenURL, secret string) ServiceAccountKey {
	return serviceAccountHash([]string{"service-account", profile, clientID, tokenURL, secret})
}

func serviceAccountHash(fields []string) ServiceAccountKey {
	h := sha256.New()
	for _, value := range fields {
		_, _ = fmt.Fprintf(h, "%d:%s", len(value), value)
	}
	var key ServiceAccountKey
	copy(key.digest[:], h.Sum(nil))
	return key
}

func (k ServiceAccountKey) Hash() string               { return hex.EncodeToString(k.digest[:]) }
func (ServiceAccountKey) String() string               { return "[redacted]" }
func (ServiceAccountKey) GoString() string             { return "[redacted]" }
func (ServiceAccountKey) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte("[redacted]")) }
func (ServiceAccountKey) LogValue() slog.Value         { return slog.StringValue("[redacted]") }
func (ServiceAccountKey) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (ServiceAccountKey) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

// GetServiceAccount uses the shared cache sequence with strict file checks
// and a separate namespace. An ended caller context takes precedence.
func (c *Cache) GetServiceAccount(ctx context.Context, key ServiceAccountKey, rejected string, login func(context.Context) (Token, error)) (Token, error) {
	if err := ctx.Err(); err != nil {
		return Token{}, err
	}
	namespace := filepath.Join(c.dir, "service-account-v1")
	for _, dir := range []string{c.dir, namespace} {
		if err := prepareServiceAccountDir(dir); err != nil {
			return Token{}, cacheUnavailable(ctx)
		}
	}
	stem := filepath.Join(namespace, key.Hash())
	return c.getEntry(ctx, stem+".json", stem+".lock", rejected, login, true)
}

func cacheUnavailable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}

func prepareServiceAccountDir(path string) error {
	info, err := os.Lstat(path) //nolint:gosec // path is the configured cache directory, its fixed namespace, or a digest-named entry.
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil { //nolint:gosec // path is the caller-selected cache directory or its fixed namespace.
			return err
		}
		info, err = os.Lstat(path) //nolint:gosec // Recheck the configured cache directory or its fixed namespace after creation.
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return ErrUnavailable
	}
	return nil
}

func validateServiceAccountInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return ErrUnavailable
	}
	return nil
}

func validateServiceAccountFile(path string, file *os.File) error {
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateServiceAccountInfo(opened); err != nil {
		return err
	}
	named, err := os.Lstat(path) //nolint:gosec // The digest-named entry is checked against the opened inode.
	if err != nil {
		return err
	}
	if err := validateServiceAccountInfo(named); err != nil {
		return err
	}
	if !os.SameFile(opened, named) {
		return ErrUnavailable
	}
	return nil
}

func checkServiceAccountPath(path string, create bool) error {
	info, err := os.Lstat(path) //nolint:gosec // path is the configured cache directory, its fixed namespace, or a digest-named entry.
	if create && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return validateServiceAccountInfo(info)
}

func (c *Cache) readServiceAccountFresh(path, rejected string) (fileContent, bool, error) {
	file, err := openServiceAccountFile(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return fileContent{}, false, nil
	}
	if err != nil {
		return fileContent{}, false, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return fileContent{}, false, err
	}
	if err := validateServiceAccountFile(path, file); err != nil {
		return fileContent{}, false, err
	}
	if len(raw) > 64<<10 {
		return fileContent{}, false, nil
	}
	var fc fileContent
	if err := json.Unmarshal(raw, &fc); err != nil {
		return fileContent{}, false, nil
	}
	return fc, c.fresh(fc, rejected), nil
}
