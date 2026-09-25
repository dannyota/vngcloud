// Package tokencache stores one access token per credential set on disk, so
// several processes sharing one profile's credentials log in only once and
// each later process reuses the cached token until it nears expiry.
package tokencache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Token is an access token, its expiry, and the time it was obtained. Get
// sets ObtainedAt itself, from disk on a cache hit or from its own clock on a
// fresh login; a login callback's returned Token need not set it.
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
	ObtainedAt  time.Time
}

// Key identifies one cached credential set. Any differing field is a
// separate cache entry.
type Key struct {
	Profile   string
	RootEmail string
	Username  string
	SigninURL string
	TokenURL  string
}

// Hash is the cache entry's file name stem: SHA-256 hex over each field of
// Key in order, length-prefixed so no combination of field values can
// collide with another (Key{"ab","c"} and Key{"a","bc"} hash differently).
func (k Key) Hash() string {
	h := sha256.New()
	for _, v := range []string{k.Profile, k.RootEmail, k.Username, k.SigninURL, k.TokenURL} {
		// hash.Hash.Write never returns a non-nil error, per its documented
		// contract.
		_, _ = fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

const (
	minBackoff  = 10 * time.Millisecond
	maxBackoff  = 500 * time.Millisecond
	freshWindow = 30 * time.Second
)

// fileContent is the on-disk shape of one cache entry.
type fileContent struct {
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	ObtainedAt  time.Time `json:"obtainedAt"`
}

// Cache stores tokens under a directory, one file per Key. now is called for
// every freshness check and for stamping a written token's obtain time, so
// tests can inject a fake clock.
type Cache struct {
	dir string
	now func() time.Time
}

// New returns a Cache rooted at dir. dir is created, with mode 0700, on
// first use rather than by New itself.
func New(dir string, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{dir: dir, now: now}
}

// Get returns a usable token for key, calling login only when the cache has
// none for key, the cached one is expiring within 30 seconds, or it equals
// rejected and has been held at least 30 seconds (the caller passes
// rejected after a 401; a token still under 30 seconds old is reused as is,
// so a rejection that a new login cannot fix ends in one failed retry
// instead of a login inside the same 30-second TOTP window).
//
// The whole read-check-login-write sequence runs under one lock on
// <dir>/<hash>.lock, so two processes sharing key never log in for the same
// request. The lock is tried without blocking, then retried with backoff up
// to 500ms until ctx ends, at which point Get returns ctx.Err().
func (c *Cache) Get(ctx context.Context, key Key, rejected string, login func(context.Context) (Token, error)) (Token, error) {
	if err := prepareDir(c.dir); err != nil {
		return Token{}, err
	}
	hash := key.Hash()
	tokenPath := filepath.Join(c.dir, hash+".json")
	lockPath := filepath.Join(c.dir, hash+".lock")

	unlock, err := acquireLock(ctx, lockPath, minBackoff, maxBackoff)
	if err != nil {
		return Token{}, err
	}
	defer unlock()

	if fc, ok := c.readFresh(tokenPath, rejected); ok {
		return Token(fc), nil
	}

	token, err := login(ctx)
	if err != nil {
		return Token{}, err
	}
	obtainedAt := c.now()
	c.write(tokenPath, fileContent{AccessToken: token.AccessToken, ExpiresAt: token.ExpiresAt, ObtainedAt: obtainedAt})
	token.ObtainedAt = obtainedAt
	return token, nil
}

// readFresh reports whether tokenPath holds a token that Get should reuse:
// it exists, parses, is not within 30 seconds of expiry, and is not both
// equal to rejected and old enough (see rejectable) to trust the rejection.
func (c *Cache) readFresh(tokenPath, rejected string) (fileContent, bool) {
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return fileContent{}, false
	}
	var fc fileContent
	if err := json.Unmarshal(data, &fc); err != nil || fc.AccessToken == "" {
		return fileContent{}, false
	}
	if fc.ExpiresAt.Sub(c.now()) <= freshWindow {
		return fileContent{}, false
	}
	if rejected != "" && fc.AccessToken == rejected && rejectable(c.now(), fc.ObtainedAt) {
		return fileContent{}, false
	}
	return fc, true
}

// rejectable reports whether a token obtained at obtainedAt has been held
// long enough that a rejection of it should be trusted: at least
// freshWindow, or obtainedAt is after now. The clock moving backward (a
// system time correction) must not be read as "just obtained", since that
// would make an already-rejected token permanently un-invalidatable.
func rejectable(now, obtainedAt time.Time) bool {
	age := now.Sub(obtainedAt)
	return age < 0 || age >= freshWindow
}

// write saves fc to tokenPath through a temp file and rename, both under
// dir. Rename replaces whatever is at tokenPath, symlink or not, by inode
// rather than by opening the path, so a symlink placed there cannot redirect
// the write to another file; the symlink itself does not survive the
// replacement. Any failure along the way removes the temp file; the caller
// already has a good token from login, so a cache write failure is not
// returned as an error.
func (c *Cache) write(tokenPath string, fc fileContent) {
	dir := filepath.Dir(tokenPath)
	tmp, err := os.CreateTemp(dir, ".tok-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(name)
		}
	}()

	data, err := json.Marshal(fc) //nolint:gosec // writing the token to this cache file (mode 0600) is this type's purpose
	if err != nil {
		_ = tmp.Close()
		return
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return
	}
	if err := os.Rename(name, tokenPath); err != nil {
		return
	}
	committed = true
}

// prepareDir creates dir with mode 0700 when missing, and refuses a
// pre-existing directory that group or others can access. The check is
// skipped on Windows, whose permission model does not map onto Unix mode
// bits.
func prepareDir(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("tokencache: %s is not a directory", dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("tokencache: %s is accessible by group or others; chmod 700", dir)
	}
	return nil
}
