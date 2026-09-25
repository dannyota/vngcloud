package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// configureKey describes one configure key: which file it lives in and
// whether it is a secret. A secret is masked by get and list, and can only
// ever be set from stdin (configure set <key> -), never a literal argv
// value, so it never appears in argv, ps output, or shell history.
type configureKey struct {
	inCredentials bool
	secret        bool
}

var configureKeys = map[string]configureKey{
	"region":      {},
	"project_id":  {},
	"output":      {},
	"read_only":   {},
	"root_email":  {inCredentials: true},
	"username":    {inCredentials: true},
	"password":    {inCredentials: true, secret: true},
	"totp_secret": {inCredentials: true, secret: true},
}

// configureKeyOrder is the fixed prompt and listing order for configure and
// configure list.
var configureKeyOrder = []string{
	"region", "project_id", "output", "read_only",
	"root_email", "username", "password", "totp_secret",
}

var configureLabels = map[string]string{
	"region": "Region", "project_id": "Project ID", "output": "Output format",
	"read_only": "Read only", "root_email": "Root email", "username": "Username",
	"password": "Password", "totp_secret": "TOTP secret",
}

// maxConfigureStdinValue caps configure set <key> -'s stdin read, so a
// mistakenly huge input cannot exhaust memory.
const maxConfigureStdinValue = 4096

func fileFor(key string) (envVar, defaultName string) {
	if configureKeys[key].inCredentials {
		return envCredsFileVar, defaultCredsName
	}
	return envConfigFileVar, defaultConfigName
}

// sectionFor returns key's section in its file for profile: the bare
// profile name in the credentials file, or configSectionName(profile) in the
// config file (matching LoadConfig's own convention).
func sectionFor(key, profile string) string {
	if configureKeys[key].inCredentials {
		return profile
	}
	return configSectionName(profile)
}

func newConfigureCmd(e *env) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "configure",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return runConfigureInteractive(e)
		},
	}
	cmd.AddCommand(newConfigureSetCmd(e))
	cmd.AddCommand(newConfigureGetCmd(e))
	cmd.AddCommand(newConfigureListCmd(e))
	return cmd
}

func newConfigureSetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:  "set <key> <value>",
		Args: exactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return runConfigureSet(e, args[0], args[1])
		},
	}
}

func newConfigureGetCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:  "get <key>",
		Args: exactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runConfigureGet(e, args[0])
		},
	}
}

func newConfigureListCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:  "list",
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			return runConfigureList(e)
		},
	}
}

// exactArgs is cobra.ExactArgs(n) wrapped so its error is a usageError, like
// noArgs.
func exactArgs(n int) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageError{msg: err.Error()}
		}
		return nil
	}
}

// validateConfigureValue checks the two keys with a fixed set of accepted
// values; every other key accepts anything, since its own consumer (the SDK,
// or the CLI's flags) validates it, and configure only needs to keep the
// file well-formed.
func validateConfigureValue(key, value string) error {
	switch key {
	case "read_only":
		if _, ok := parseOnOff(value); !ok {
			return newUsageError("read_only must be 1, true, 0, false, or empty, got %q", value)
		}
	case "output":
		if value != "" && !validOutputFormats[value] {
			return newUsageError("output must be json, table, or text, got %q", value)
		}
	}
	return nil
}

// validateProfileName rejects a profile name that could inject a new section
// or key into a config or credentials file: a carriage return or newline, a
// literal '[' or ']' (INI's own section-header syntax), or leading or
// trailing whitespace, which a section header itself would have trimmed away
// before comparison, so accepting it here could resolve to a different
// section than the one the caller named. Checked before any configure
// command reads or writes a file, so a hostile --profile or VNGCLOUD_PROFILE
// value can never reach setINIValue or readINIValue.
func validateProfileName(name string) error {
	if strings.ContainsAny(name, "\r\n[]") || name != strings.TrimSpace(name) {
		return newUsageError("profile name must not contain a newline, '[', ']', or leading or trailing whitespace")
	}
	return nil
}

// checkReadOnlyNotTurnedOff refuses newValue when it would turn read_only
// off for a profile that currently has it on, per the CLI design's
// "Configure under read-only". A current value the CLI cannot parse is
// treated as off here; LoadConfig itself would separately refuse the file
// as invalid.
func checkReadOnlyNotTurnedOff(envVar, defaultName, section, newValue string) error {
	current, err := currentConfigureValue(envVar, defaultName, section, "read_only")
	if err != nil {
		return err
	}
	curOn, _ := parseOnOff(current)
	newOn, _ := parseOnOff(newValue)
	if curOn && !newOn {
		return newUsageError("read_only is on for this profile; edit the file by hand to turn it off")
	}
	return nil
}

func runConfigureSet(e *env, key, value string) error {
	if on, source, err := readOnlyPreConfig(e.flags); err != nil {
		return err
	} else if on {
		return readOnlyError{source: source}
	}

	meta, ok := configureKeys[key]
	if !ok {
		return newUsageError("unknown configure key %q", key)
	}

	if meta.secret && value != "-" {
		return newUsageError("%s must be set from stdin: configure set %s -", key, key)
	}

	if value == "-" {
		if isTerminal(e.stdin) {
			return newUsageError("configure set %s - refuses a terminal on stdin; use configure instead", key)
		}
		read, err := readStdinValue(e.stdin)
		if err != nil {
			return err
		}
		if read == "" {
			return newUsageError("configure set %s -: stdin had no value", key)
		}
		value = read
	}

	if err := validateConfigureValue(key, value); err != nil {
		return err
	}

	profile := resolvedProfileName(e.flags)
	if err := validateProfileName(profile); err != nil {
		return err
	}
	envVar, defaultName := fileFor(key)
	section := sectionFor(key, profile)

	if key == "read_only" {
		if err := checkReadOnlyNotTurnedOff(envVar, defaultName, section, value); err != nil {
			return err
		}
	}
	return setConfigureValue(envVar, defaultName, section, key, value)
}

func runConfigureGet(e *env, key string) error {
	meta, ok := configureKeys[key]
	if !ok {
		return newUsageError("unknown configure key %q", key)
	}
	profile := resolvedProfileName(e.flags)
	if err := validateProfileName(profile); err != nil {
		return err
	}
	envVar, defaultName := fileFor(key)
	value, err := currentConfigureValue(envVar, defaultName, sectionFor(key, profile), key)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(e.stdout, maskIfSecret(meta, value))
	return err
}

func runConfigureList(e *env) error {
	profile := resolvedProfileName(e.flags)
	if err := validateProfileName(profile); err != nil {
		return err
	}
	for _, key := range configureKeyOrder {
		meta := configureKeys[key]
		envVar, defaultName := fileFor(key)
		value, err := currentConfigureValue(envVar, defaultName, sectionFor(key, profile), key)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(e.stdout, "%s = %s\n", key, maskIfSecret(meta, value)); err != nil {
			return err
		}
	}
	return nil
}

func maskIfSecret(meta configureKey, value string) string {
	if meta.secret && value != "" {
		return "****"
	}
	return value
}

// readStdinValue reads all of r (capped at maxConfigureStdinValue+1 bytes,
// so an oversized input is detected rather than silently truncated) and
// drops one trailing newline, "\n" or "\r\n".
func readStdinValue(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxConfigureStdinValue+1))
	if err != nil {
		return "", newUsageError("reading stdin: %s", err)
	}
	if len(data) > maxConfigureStdinValue {
		return "", newUsageError("stdin value is larger than %d bytes", maxConfigureStdinValue)
	}
	s := string(data)
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return s[:len(s)-2], nil
	case strings.HasSuffix(s, "\n"):
		return s[:len(s)-1], nil
	default:
		return s, nil
	}
}

func runConfigureInteractive(e *env) error {
	if on, source, err := readOnlyPreConfig(e.flags); err != nil {
		return err
	} else if on {
		return readOnlyError{source: source}
	}
	if !isTerminal(e.stdin) {
		return newUsageError("configure needs a terminal on stdin; use configure set instead")
	}

	profile := resolvedProfileName(e.flags)
	if err := validateProfileName(profile); err != nil {
		return err
	}
	in := bufio.NewReader(e.stdin)

	for _, key := range configureKeyOrder {
		envVar, defaultName := fileFor(key)
		section := sectionFor(key, profile)
		current, err := currentConfigureValue(envVar, defaultName, section, key)
		if err != nil {
			return err
		}

		var value string
		if configureKeys[key].secret {
			value, err = promptSecret(e, configureLabels[key], current != "")
		} else {
			value, err = promptLine(e, in, configureLabels[key], current)
		}
		if err != nil {
			return err
		}
		if value == "" {
			continue // blank keeps the existing value
		}
		if err := validateConfigureValue(key, value); err != nil {
			return err
		}
		if key == "read_only" {
			if err := checkReadOnlyNotTurnedOff(envVar, defaultName, section, value); err != nil {
				return err
			}
		}
		if err := setConfigureValue(envVar, defaultName, section, key, value); err != nil {
			return err
		}
	}
	return nil
}

func promptLine(e *env, in *bufio.Reader, label, current string) (string, error) {
	if _, err := fmt.Fprintf(e.stdout, "%s [%s]: ", label, current); err != nil {
		return "", err
	}
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func promptSecret(e *env, label string, hasCurrent bool) (string, error) {
	shown := ""
	if hasCurrent {
		shown = "****"
	}
	if _, err := fmt.Fprintf(e.stdout, "%s [%s]: ", label, shown); err != nil {
		return "", err
	}
	secret, err := readSecret(e)
	// term.ReadPassword consumes the terminating Enter without echoing it,
	// so the prompt's own newline is written here regardless of outcome.
	_, _ = fmt.Fprintln(e.stdout)
	if err != nil {
		return "", err
	}
	return secret, nil
}

// isTerminalOverride, set only from a _test.go file in this package, lets a
// test force isTerminal's result without a real terminal or pseudo-terminal,
// which the test environment may not have. It is always nil in the built
// binary.
var isTerminalOverride func(io.Reader) bool

// isTerminal reports whether r is an interactive terminal. Only an *os.File
// can be: e.stdin is always os.Stdin in the built binary, but a test's
// strings.Reader or bytes.Buffer is correctly never one.
func isTerminal(r io.Reader) bool {
	if isTerminalOverride != nil {
		return isTerminalOverride(r)
	}
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// readSecretOverride, set only from a _test.go file in this package,
// replaces term.ReadPassword so the interactive configure command can be
// tested without a real terminal. It is always nil in the built binary.
var readSecretOverride func(*env) (string, error)

// readSecret reads one line from e.stdin without echoing it. e.stdin must be
// an *os.File (isTerminal is always checked first, and only an *os.File can
// be a terminal), since term.ReadPassword reads the raw file descriptor
// directly rather than through the io.Reader interface.
func readSecret(e *env) (string, error) {
	if readSecretOverride != nil {
		return readSecretOverride(e)
	}
	f, ok := e.stdin.(*os.File)
	if !ok {
		return "", errors.New("cli: stdin is not a terminal")
	}
	data, err := term.ReadPassword(int(f.Fd()))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
