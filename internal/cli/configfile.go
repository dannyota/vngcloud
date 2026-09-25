package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"danny.vn/vngcloud"
)

// The two profile files' environment overrides, and the default names under
// ~/.vngcloud/; both are also documented in the SDK's Configuration wiki
// page. configure resolves paths with the same names and precedence
// LoadConfig uses, without importing it, since configure edits files rather
// than loading a Config.
const (
	envConfigFileVar = "VNGCLOUD_CONFIG_FILE"
	envCredsFileVar  = "VNGCLOUD_SHARED_CREDENTIALS_FILE"

	defaultConfigName = "config"
	defaultCredsName  = "credentials"
)

// resolveConfigureFilePath resolves the path configure writes for one file:
// envVar's value when set, else the default location under the home
// directory's .vngcloud directory. explicit is true only for the envVar
// case, matching LoadConfig's own distinction: a missing parent directory is
// an error only when the path came from an explicit source.
func resolveConfigureFilePath(envVar, defaultName string) (path string, explicit bool, err error) {
	if v := os.Getenv(envVar); v != "" {
		return v, true, nil
	}
	home, herr := os.UserHomeDir()
	if herr != nil || home == "" {
		return "", false, fmt.Errorf("%w: cannot resolve the home directory", vngcloud.ErrInvalidConfig)
	}
	return filepath.Join(home, ".vngcloud", defaultName), false, nil
}

// configSectionName is the config file's section for profile: "default" is
// unprefixed, and every other profile uses the "profile <name>" prefix,
// matching LoadConfig's own convention (see the SDK's Configuration wiki
// page).
func configSectionName(profile string) string {
	if profile == "default" {
		return "default"
	}
	return "profile " + profile
}

// ensureParentDir prepares path's directory before the first write to it: a
// default (non-explicit) path gets ~/.vngcloud created with mode 0700 if
// missing; an explicit (environment-named) path's missing parent is an
// error, since configure never creates a directory a variable pointed at
// deliberately.
func ensureParentDir(path string, explicit bool) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if explicit {
			return fmt.Errorf("%w: %s: parent directory does not exist", vngcloud.ErrInvalidConfig, path)
		}
		return os.MkdirAll(dir, 0o700)
	case err != nil:
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, path, err)
	case !info.IsDir():
		return fmt.Errorf("%w: %s is not a directory", vngcloud.ErrInvalidConfig, dir)
	}
	return nil
}

// writeTarget is what loadForEdit resolves before an edit: renamePath is
// where the new content is ultimately renamed to (the resolved path, so a
// symlink at the original path stays a symlink and gets new content instead
// of being replaced by a regular file); dir is renamePath's directory, where
// the temp file is created; content is the existing file's bytes, or nil for
// a path with nothing there yet; mode is unused today (both files always end
// at 0600 regardless of any prior mode) but kept for clarity at call sites.
type writeTarget struct {
	renamePath string
	dir        string
	content    []byte
}

// loadForEdit resolves path for a configure write: it refuses a dangling
// symlink and a target that is not a regular file, both checked from an open
// file handle rather than a separate stat, closing the window in which the
// path could be swapped for a FIFO or other special file between a check and
// an open. A path with nothing at it yet is not an error: content is nil,
// ready for a fresh file.
func loadForEdit(path string) (writeTarget, error) {
	renamePath := path
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, everr := filepath.EvalSymlinks(path)
		if everr != nil {
			return writeTarget{}, fmt.Errorf("%w: %s is a dangling symlink", vngcloud.ErrInvalidConfig, path)
		}
		renamePath = resolved
	}

	f, err := openConfigureFile(renamePath)
	if errors.Is(err, os.ErrNotExist) {
		return writeTarget{renamePath: renamePath, dir: filepath.Dir(renamePath)}, nil
	}
	if err != nil {
		return writeTarget{}, fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, renamePath, err)
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return writeTarget{}, fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, renamePath, err)
	}
	if !st.Mode().IsRegular() {
		return writeTarget{}, fmt.Errorf("%w: %s is not a regular file", vngcloud.ErrInvalidConfig, renamePath)
	}
	content, err := readAllLimited(f, maxConfigureFileSize)
	if err != nil {
		return writeTarget{}, fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, renamePath, err)
	}
	return writeTarget{renamePath: renamePath, dir: filepath.Dir(renamePath), content: content}, nil
}

// maxConfigureFileSize caps how much of an existing config or credentials
// file configure reads back before editing it, so a mistakenly huge file
// cannot exhaust memory.
const maxConfigureFileSize = 1 << 20 // 1 MiB

func readAllLimited(f *os.File, limit int64) ([]byte, error) {
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		data = append(data, buf[:n]...)
		if int64(len(data)) > limit {
			return nil, fmt.Errorf("file is larger than %d bytes", limit)
		}
		if errors.Is(err, io.EOF) {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// writeAtomic writes content to a temp file in target.dir, mode 0600, syncs
// and closes it, then renames it over target.renamePath: both files end at
// 0600 regardless of any prior mode, a symlinked file stays a symlink
// (renamePath is already the resolved target, never the symlink itself), and
// a crash leaves either the old or the new content, never a partial file.
// The temp file is removed on every failure path.
func writeAtomic(target writeTarget, content []byte) error {
	tmp, err := os.CreateTemp(target.dir, ".vngcloud-configure-*")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, target.dir, err)
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(name)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, target.renamePath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, target.renamePath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, target.renamePath, err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, target.renamePath, err)
	}
	if err := os.Rename(name, target.renamePath); err != nil {
		return fmt.Errorf("%w: %s: %w", vngcloud.ErrInvalidConfig, target.renamePath, err)
	}
	committed = true
	return nil
}

// bomPrefix is the UTF-8 encoding of U+FEFF, matching internal/ini's own
// leading-byte-order-mark handling exactly (see ini.Parse), so a BOM-prefixed
// file parses to the same section and key values here as it does through
// LoadConfig: without stripping it, "[default]" preceded by a BOM would never
// match a bare "[" section-header check, silently hiding every key already
// in the file (including read_only) from setINIValue and readINIValue alike.
var bomPrefix = []byte{0xEF, 0xBB, 0xBF}

// splitBOM reports whether content starts with a UTF-8 BOM and returns the
// remainder to parse; the BOM itself is never part of section or key
// matching, but setINIValue writes it back unchanged at the front of the
// result so a BOM-prefixed file keeps its BOM across an edit.
func splitBOM(content []byte) (hasBOM bool, rest []byte) {
	if bytes.HasPrefix(content, bomPrefix) {
		return true, content[len(bomPrefix):]
	}
	return false, content
}

// setINIValue returns content with key set to value inside [section],
// keeping every other line, comments included, and keeping a leading UTF-8
// BOM when content has one (see splitBOM). Section names are matched exactly
// as internal/ini trims them, and keys are matched case-insensitively, both
// mirroring the SDK's own parser. The section name may appear more than once
// in the file (internal/ini merges every occurrence into one logical
// section); when the key appears more than once across those occurrences,
// the last one by file position is changed, since that is the one the
// map-building parser ends up holding, and a new key is added to the last
// occurrence of the section. A section absent from the file is appended at
// the end.
func setINIValue(content []byte, section, key, value string) []byte {
	hasBOM, body := splitBOM(content)
	result := setINIValueBody(body, section, key, value)
	if !hasBOM {
		return result
	}
	out := make([]byte, 0, len(bomPrefix)+len(result))
	out = append(out, bomPrefix...)
	out = append(out, result...)
	return out
}

func setINIValueBody(content []byte, section, key, value string) []byte {
	keyLower := strings.ToLower(key)
	newLine := key + " = " + value

	var lines []string
	if len(content) > 0 {
		lines = strings.Split(string(content), "\n")
	}

	// occurrence.insertAt is where a brand-new key belongs for that
	// occurrence of the section: right after its last key line, or right
	// after the header itself when the occurrence has no keys yet. A blank
	// or comment line never advances it, so it always lands before any
	// blank or comment lines trailing the section, even when those sit
	// immediately before the next section's header.
	type occurrence struct{ insertAt int }
	var occurrences []occurrence
	curSection, curHeader := "", -1
	curInsertAt := -1
	lastKeyLine := -1
	lastKeyPrefix := ""

	closeCurrent := func() {
		if curSection == section && curHeader >= 0 {
			occurrences = append(occurrences, occurrence{insertAt: curInsertAt})
		}
	}

	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";"):
			continue
		case strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"):
			closeCurrent()
			curSection = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			curHeader = i
			curInsertAt = i + 1
		default:
			if curSection != section {
				continue
			}
			curInsertAt = i + 1
			if idx := strings.Index(line, "="); idx >= 0 {
				k := strings.ToLower(strings.TrimSpace(line[:idx]))
				if k == keyLower {
					lastKeyLine = i
					lastKeyPrefix = line[:idx+1]
				}
			}
		}
	}
	closeCurrent()

	switch {
	case lastKeyLine >= 0:
		lines[lastKeyLine] = lastKeyPrefix + " " + value
		return []byte(strings.Join(ensureTrailingNewline(lines), "\n"))
	case len(occurrences) > 0:
		insertAt := occurrences[len(occurrences)-1].insertAt
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAt]...)
		out = append(out, newLine)
		out = append(out, lines[insertAt:]...)
		return []byte(strings.Join(ensureTrailingNewline(out), "\n"))
	default:
		out := append([]string{}, lines...)
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "["+section+"]", newLine)
		return []byte(strings.Join(ensureTrailingNewline(out), "\n"))
	}
}

// ensureTrailingNewline returns lines with one more empty element appended
// when it does not already end with one, so strings.Join(..., "\n") always
// produces content ending in a single "\n": Join leaves no trailing
// separator after its last element, so a lines slice that does not already
// end with "" (the marker a trailing "\n" in the original content leaves
// behind after Split) would otherwise silently drop the file's final
// newline.
func ensureTrailingNewline(lines []string) []string {
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return lines
	}
	return append(lines, "")
}

// readINIValue returns key's value inside section (matched the same way
// setINIValue matches it: a leading UTF-8 BOM stripped first, section names
// trimmed as internal/ini trims them, keys case-insensitively, the last
// occurrence winning), or "" when the section or key is absent.
func readINIValue(content []byte, section, key string) string {
	_, content = splitBOM(content)
	if len(content) == 0 {
		return ""
	}
	keyLower := strings.ToLower(key)
	curSection := ""
	value := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";"):
			continue
		case strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"):
			curSection = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		default:
			if curSection != section {
				continue
			}
			if idx := strings.Index(line, "="); idx >= 0 {
				if strings.ToLower(strings.TrimSpace(line[:idx])) == keyLower {
					value = strings.TrimSpace(line[idx+1:])
				}
			}
		}
	}
	return value
}

// setConfigureValue resolves path, prepares its directory and any symlink,
// edits key in section to value, and writes the result back atomically. kind
// names the file for error messages ("config" or "credentials").
//
// It refuses, before touching anything, a value holding a newline: the INI
// format has no quoting, so an embedded newline would let a value inject a
// new key or a whole new section into the file. This is checked here, the
// single path every caller (configure set and the interactive configure
// prompt) writes through, rather than in each caller.
func setConfigureValue(envVar, defaultName, section, key, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return newUsageError("%s cannot contain a newline", key)
	}
	path, explicit, err := resolveConfigureFilePath(envVar, defaultName)
	if err != nil {
		return err
	}
	if err := ensureParentDir(path, explicit); err != nil {
		return err
	}
	target, err := loadForEdit(path)
	if err != nil {
		return err
	}
	newContent := setINIValue(target.content, section, key, value)
	return writeAtomic(target, newContent)
}

// currentConfigureValue reads key's current value from section without
// creating or modifying anything; used by configure get, configure list, and
// the check that refuses to turn read_only off.
func currentConfigureValue(envVar, defaultName, section, key string) (string, error) {
	path, _, err := resolveConfigureFilePath(envVar, defaultName)
	if err != nil {
		return "", err
	}
	target, err := loadForEdit(path)
	if err != nil {
		return "", err
	}
	return readINIValue(target.content, section, key), nil
}
