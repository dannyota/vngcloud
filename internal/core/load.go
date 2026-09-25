package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"danny.vn/vngcloud/internal/ini"
)

const (
	envProfile     = "VNGCLOUD_PROFILE"
	envRegion      = "VNGCLOUD_REGION"
	envProjectID   = "VNGCLOUD_PROJECT_ID"
	envRootEmail   = "VNGCLOUD_ROOT_EMAIL"
	envUsername    = "VNGCLOUD_USERNAME"
	envPassword    = "VNGCLOUD_PASSWORD"
	envTOTPSecret  = "VNGCLOUD_TOTP_SECRET"
	envAccessToken = "VNGCLOUD_ACCESS_TOKEN"
	envConfigFile  = "VNGCLOUD_CONFIG_FILE"
	envCredsFile   = "VNGCLOUD_SHARED_CREDENTIALS_FILE"

	defaultProfile = "default"
)

// LoadConfig resolves a Config from opts, environment variables, and the
// AWS-style profile files ~/.vngcloud/config and ~/.vngcloud/credentials
// (or their WithConfigFile/WithSharedCredentialsFile/env-var overrides).
// Highest precedence first: opts, then environment variables, then the
// resolved profile's file sections. An explicit profile (WithProfile, not
// VNGCLOUD_PROFILE) skips environment variables for credentials and the
// project ID, so a stray environment value for one account can never send
// a call made with another profile to that account. Region is not
// account-specific, so it may still come from the environment even with an
// explicit profile.
//
// An empty value from an option, an environment variable, or a file key
// counts as unset. LoadConfig returns ctx.Err() directly, unwrapped, when
// ctx is already canceled or past its deadline. Every other error matches
// ErrInvalidConfig via errors.Is; a missing or incomplete credential set
// also matches ErrNoCredentials, and a credentials file problem also
// matches ErrCredentialsFile. A token cache directory refused for unsafe
// permissions is a separate error surfaced later, at the first call that
// needs a token, not from LoadConfig itself.
func LoadConfig(ctx context.Context, opts ...Option) (Config, error) {
	if err := ctx.Err(); err != nil {
		return Config{}, err
	}

	settings := defaultClientConfig()
	for _, opt := range opts {
		opt.apply(&settings)
	}

	explicitProfile := settings.profile != ""
	profile := settings.profile
	if !explicitProfile {
		profile = os.Getenv(envProfile)
	}
	if profile == "" {
		profile = defaultProfile
	}

	defaultConfigPath, defaultCredsPath := defaultFilePaths()
	configFile, err := loadConfigFile(resolveFileSource(settings.configFile, envConfigFile, defaultConfigPath))
	if err != nil {
		return Config{}, err
	}
	credsFile, err := loadCredentialsFile(resolveFileSource(settings.credentialsFile, envCredsFile, defaultCredsPath))
	if err != nil {
		return Config{}, err
	}

	if err := checkProfileExists(profile, configFile, credsFile); err != nil {
		return Config{}, err
	}

	configSection, _ := configFile.Section(configSectionName(profile))
	credsSection, _ := credsFile.Section(profile)

	region, err := resolveRegion(settings.region, configSection)
	if err != nil {
		return Config{}, err
	}
	settings.region = region
	settings.projectID = resolveProjectID(settings.projectID, explicitProfile, configSection)

	if err := resolveCredentials(&settings, explicitProfile, profile, credsSection); err != nil {
		return Config{}, err
	}
	settings.profile = profile

	c, err := buildClient(settings)
	if err != nil {
		return Config{}, err
	}
	return Config{client: c}, nil
}

// defaultFilePaths returns the default config and credentials paths under
// the resolved home directory, or two empty strings when the home
// directory cannot be determined, so a default (non-explicit) file is
// simply skipped rather than erroring.
func defaultFilePaths() (configPath, credsPath string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", ""
	}
	return filepath.Join(home, ".vngcloud", "config"), filepath.Join(home, ".vngcloud", "credentials")
}

// fileSource is one resolved file path, and whether it came from an
// explicit option or environment variable rather than a default location.
type fileSource struct {
	path     string
	explicit bool
}

// resolveFileSource applies the Paths precedence for one file: optionValue,
// then the named environment variable, then defaultPath. Only the default
// is not explicit, so only it is skipped when the file does not exist.
func resolveFileSource(optionValue, envVar, defaultPath string) fileSource {
	if optionValue != "" {
		return fileSource{path: optionValue, explicit: true}
	}
	if v := os.Getenv(envVar); v != "" {
		return fileSource{path: v, explicit: true}
	}
	return fileSource{path: defaultPath}
}

func loadConfigFile(src fileSource) (ini.File, error) {
	return loadIniFile(src, ErrInvalidConfig, false)
}

func loadCredentialsFile(src fileSource) (ini.File, error) {
	return loadIniFile(src, ErrCredentialsFile, true)
}

// loadIniFile opens and parses src, applying the shared file-safety rules.
// A missing default path is not an error; anything else wrong with the path
// is wrapped in sentinelErr and names only the path, never its content.
// When checkMode is set (the credentials file), an open file that group or
// others can read is refused before it is parsed.
//
// It opens src.path first and only then judges its type and mode, from the
// open file descriptor rather than a separate path-based stat: checking the
// path first and opening it second would leave a window between the two in
// which the path could be swapped for a FIFO or other special file, so a
// check that saw a regular file would not be the one the open actually
// reads. openConfigFile closes that window by opening first (on Unix,
// without blocking, so a FIFO swapped into place cannot hang this call), and
// f.Stat() below reports the type and mode of the exact file this call has
// open, not whatever is currently at the path.
func loadIniFile(src fileSource, sentinelErr error, checkMode bool) (ini.File, error) {
	if src.path == "" {
		return ini.File{}, nil
	}

	f, err := openConfigFile(src.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !src.explicit {
			return ini.File{}, nil
		}
		return nil, fmt.Errorf("%w: %s cannot be read", sentinelErr, src.path)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: %s cannot be read", sentinelErr, src.path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", sentinelErr, src.path)
	}

	if checkMode && runtime.GOOS != "windows" {
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%w: %s is accessible by group or others; chmod 600", sentinelErr, src.path)
		}
	}

	parsed, err := ini.Parse(src.path, f)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", sentinelErr, err)
	}
	return parsed, nil
}

// configSectionName is the config file's section for profile: "default" is
// unprefixed, and every other profile uses the "profile <name>" prefix.
// The credentials file always uses the bare profile name instead.
func configSectionName(profile string) string {
	if profile == defaultProfile {
		return defaultProfile
	}
	return "profile " + profile
}

// checkProfileExists requires a non-default profile to appear in at least
// one file, so a typo in a profile name fails clearly instead of quietly
// falling through to a missing-region or missing-credentials error that
// does not mention the profile at all.
func checkProfileExists(profile string, configFile, credsFile ini.File) error {
	if profile == defaultProfile {
		return nil
	}
	_, inConfig := configFile.Section(configSectionName(profile))
	_, inCreds := credsFile.Section(profile)
	if !inConfig && !inCreds {
		return fmt.Errorf("%w: profile %q not found in the config or credentials file", ErrInvalidConfig, profile)
	}
	return nil
}

// resolveRegion applies the Region precedence: optionValue, then
// VNGCLOUD_REGION, then the profile's config section.
func resolveRegion(optionValue string, configSection map[string]string) (string, error) {
	if optionValue != "" {
		return optionValue, nil
	}
	if v := os.Getenv(envRegion); v != "" {
		return v, nil
	}
	if v := configSection["region"]; v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%w: region is required: set WithRegion, %s, or region in the profile", ErrInvalidConfig, envRegion)
}

// resolveProjectID applies the Project ID precedence: optionValue, then (only
// when the profile is not explicit) VNGCLOUD_PROJECT_ID, then the profile's
// config section. A project ID is optional, so nothing anywhere is not an
// error.
func resolveProjectID(optionValue string, explicitProfile bool, configSection map[string]string) string {
	if optionValue != "" {
		return optionValue
	}
	if !explicitProfile {
		if v := os.Getenv(envProjectID); v != "" {
			return v
		}
	}
	return configSection["project_id"]
}

// resolveCredentials fills settings.staticToken or settings.iamUser (or
// leaves settings.credentials as the caller set it) from the first source
// that sets any credential value: options, then (only when the profile is
// not explicit) the environment, then the profile's credentials section.
// Within one source, an access token wins over IAM User values. A source
// that sets some IAM User keys but not root_email, username, and password
// (an access token needs nothing else) is an error naming the source and
// the missing keys, never a value: it is treated the same as no credentials
// at all, rather than falling through to mix in another source's values.
func resolveCredentials(settings *clientConfig, explicitProfile bool, profile string, credsSection map[string]string) error {
	if settings.credentials != nil || settings.staticToken != "" || settings.iamUser != nil {
		return nil
	}

	if !explicitProfile {
		iamUser, token, missing, ok := credentialsFromEnv()
		if ok {
			if len(missing) > 0 {
				return fmt.Errorf("%w: environment variables set some IAM User credentials but not: %s",
					ErrNoCredentials, strings.Join(missing, ", "))
			}
			if token != "" {
				settings.staticToken = token
			} else {
				settings.iamUser = iamUser
			}
			return nil
		}
	}

	iamUser, missing, ok := credentialsFromSection(credsSection)
	if ok {
		if len(missing) > 0 {
			return fmt.Errorf("%w: profile %q set some IAM User credentials but not: %s",
				ErrNoCredentials, profile, strings.Join(missing, ", "))
		}
		settings.iamUser = iamUser
		return nil
	}

	if explicitProfile {
		return fmt.Errorf("%w: profile %q has no credentials", ErrNoCredentials, profile)
	}
	return fmt.Errorf("%w: checked options, environment variables, and profile %q", ErrNoCredentials, profile)
}

// missingIAMKeys names, from keys, each required IAM User key (root_email,
// username, password; totp_secret is always optional) whose value is empty.
func missingIAMKeys(keys map[string]string) []string {
	var missing []string
	for _, key := range []string{"root_email", "username", "password"} {
		if keys[key] == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

// credentialsFromEnv reads the VNGCLOUD_* credential variables. It reports
// ok false when none of them are set. missing names, by environment variable
// rather than file key, any required key left empty when at least one IAM
// User value was set; the credentials file has no access token key, so
// VNGCLOUD_ACCESS_TOKEN is env-only and never partial.
func credentialsFromEnv() (iamUser *IAMUserAuth, accessToken string, missing []string, ok bool) {
	if v := os.Getenv(envAccessToken); v != "" {
		return nil, v, nil, true
	}
	rootEmail := os.Getenv(envRootEmail)
	username := os.Getenv(envUsername)
	password := os.Getenv(envPassword)
	totpSecret := os.Getenv(envTOTPSecret)
	if rootEmail == "" && username == "" && password == "" && totpSecret == "" {
		return nil, "", nil, false
	}
	if m := missingIAMKeys(map[string]string{"root_email": rootEmail, "username": username, "password": password}); len(m) > 0 {
		envNames := map[string]string{"root_email": envRootEmail, "username": envUsername, "password": envPassword}
		named := make([]string, len(m))
		for i, key := range m {
			named[i] = envNames[key]
		}
		return nil, "", named, true
	}
	auth := &IAMUserAuth{RootEmail: rootEmail, Username: username, Password: password}
	if totpSecret != "" {
		auth.TOTP = &SecretTOTP{Secret: totpSecret}
	}
	return auth, "", nil, true
}

// credentialsFromSection reads the IAM User keys from a credentials file
// section. It reports ok false when none of them are set. missing names, by
// file key, any required key left empty when at least one IAM User value
// was set.
func credentialsFromSection(section map[string]string) (iamUser *IAMUserAuth, missing []string, ok bool) {
	rootEmail := section["root_email"]
	username := section["username"]
	password := section["password"]
	totpSecret := section["totp_secret"]
	if rootEmail == "" && username == "" && password == "" && totpSecret == "" {
		return nil, nil, false
	}
	if m := missingIAMKeys(map[string]string{"root_email": rootEmail, "username": username, "password": password}); len(m) > 0 {
		return nil, m, true
	}
	auth := &IAMUserAuth{RootEmail: rootEmail, Username: username, Password: password}
	if totpSecret != "" {
		auth.TOTP = &SecretTOTP{Secret: totpSecret}
	}
	return auth, nil, true
}
