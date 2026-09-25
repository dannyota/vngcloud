package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"danny.vn/vngcloud"
)

// loadConfig resolves a vngcloud.Config for one operation command, per the
// CLI design's "Building the Config": --profile, --region, and --project-id
// are passed only when given, the token cache always points at
// ~/.vngcloud/cache (or is skipped entirely when the home directory or that
// directory's permissions are not safe), and a logger is attached only under
// --debug. testOptions, set only from a test, are appended last so a test's
// endpoint overrides and transport always take effect.
func loadConfig(ctx context.Context, e *env, logger *slog.Logger) (vngcloud.Config, error) {
	var opts []vngcloud.LoadOption
	if e.flags.profile != "" {
		opts = append(opts, vngcloud.WithProfile(e.flags.profile))
	}
	if e.flags.region != "" {
		opts = append(opts, vngcloud.WithRegion(e.flags.region))
	}
	if e.flags.projectID != "" {
		opts = append(opts, vngcloud.WithProjectID(e.flags.projectID))
	}

	cacheDir, err := tokenCacheDir()
	if err != nil {
		return vngcloud.Config{}, err
	}
	if cacheDir != "" {
		opts = append(opts, vngcloud.WithTokenCache(cacheDir))
	}

	if logger != nil {
		opts = append(opts, vngcloud.WithLogger(logger))
	}

	opts = append(opts, testOptions...)

	return vngcloud.LoadConfig(ctx, opts...)
}

// debugLogger returns a slog.Logger writing Debug-level text records to
// e.stderr when --debug is given, and nil otherwise. A nil logger makes
// loadConfig pass no WithLogger option at all, so the SDK logs nothing,
// exactly as a library caller who never sets one sees nothing.
func debugLogger(e *env) *slog.Logger {
	if !e.flags.debug {
		return nil
	}
	return slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// resolveOutput returns the effective --output value: the flag when given,
// else the resolved profile's own "output" setting, else "json".
func resolveOutput(flags *globalFlags, cfg vngcloud.Config) string {
	if flags.output != "" {
		return flags.output
	}
	if v := cfg.ProfileSetting("output"); v != "" {
		return v
	}
	return "json"
}

// tokenCacheDir resolves the token cache directory the CLI passes to
// WithTokenCache: "" (with a nil error) when the home directory cannot be
// found, so the CLI simply passes no cache, per the CLI design. When the
// directory already exists, its permissions are checked here, before
// LoadConfig runs, so an unsafe directory is refused as a config error (exit
// 2) rather than surfacing later as a plain, unclassified error from deep
// inside the token cache package.
func tokenCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", nil //nolint:nilerr // no home directory means the CLI passes no cache, per the CLI design; not an error
	}
	dir := filepath.Join(home, ".vngcloud", "cache")

	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return dir, nil
	case err != nil:
		// Some other stat failure (for example a parent directory that
		// cannot be traversed): let the SDK's own attempt to use the
		// directory surface the error instead of guessing here.
		return dir, nil //nolint:nilerr // deliberately deferred to the SDK, see comment above
	case !info.IsDir():
		return "", fmt.Errorf("%w: %s exists and is not a directory", vngcloud.ErrInvalidConfig, dir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%w: %s is accessible by group or others; chmod 700", vngcloud.ErrInvalidConfig, dir)
	}
	return dir, nil
}
