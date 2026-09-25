package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
// counts as unset. Every error matches ErrInvalidConfig via errors.Is; a
// missing credential set also matches ErrNoCredentials, and a credentials
// file problem also matches ErrCredentialsFile.
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
// A missing default path is not an error; anything else wrong with the
// path is wrapped in sentinelErr and names only the path, never its
// content. When checkMode is set (the credentials file), an open file that
// group or others can read is refused before it is parsed.
func loadIniFile(src fileSource, sentinelErr error, checkMode bool) (ini.File, error) {
	if src.path == "" {
		return ini.File{}, nil
	}

	if _, err := os.Lstat(src.path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !src.explicit {
			return ini.File{}, nil
		}
		return nil, fmt.Errorf("%w: %s", sentinelErr, src.path)
	}
	// os.Stat follows symlinks to their final target, so a symlinked path
	// is judged by what it ultimately resolves to, not the link itself.
	info, err := os.Stat(src.path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", sentinelErr, src.path)
	}

	f, err := os.Open(src.path) //nolint:gosec // path is an explicit option/env value or the resolved home directory, not attacker-controlled input
	if err != nil {
		return nil, fmt.Errorf("%w: %s", sentinelErr, src.path)
	}
	defer func() { _ = f.Close() }()

	if checkMode && runtime.GOOS != "windows" {
		fi, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("%w: %s", sentinelErr, src.path)
		}
		if fi.Mode().Perm()&0o077 != 0 {
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
// Within one source, an access token wins over IAM User values.
func resolveCredentials(settings *clientConfig, explicitProfile bool, profile string, credsSection map[string]string) error {
	if settings.credentials != nil || settings.staticToken != "" || settings.iamUser != nil {
		return nil
	}

	if !explicitProfile {
		if iamUser, token, ok := credentialsFromEnv(); ok {
			if token != "" {
				settings.staticToken = token
			} else {
				settings.iamUser = iamUser
			}
			return nil
		}
	}

	if iamUser, ok := credentialsFromSection(credsSection); ok {
		settings.iamUser = iamUser
		return nil
	}

	if explicitProfile {
		return fmt.Errorf("%w: profile %q has no credentials", ErrNoCredentials, profile)
	}
	return fmt.Errorf("%w: checked options, environment variables, and profile %q", ErrNoCredentials, profile)
}

// credentialsFromEnv reads the VNGCLOUD_* credential variables. It reports
// ok false when none of them are set. The credentials file has no access
// token key, so VNGCLOUD_ACCESS_TOKEN is env-only.
func credentialsFromEnv() (iamUser *IAMUserAuth, accessToken string, ok bool) {
	if v := os.Getenv(envAccessToken); v != "" {
		return nil, v, true
	}
	rootEmail := os.Getenv(envRootEmail)
	username := os.Getenv(envUsername)
	password := os.Getenv(envPassword)
	totpSecret := os.Getenv(envTOTPSecret)
	if rootEmail == "" && username == "" && password == "" && totpSecret == "" {
		return nil, "", false
	}
	auth := &IAMUserAuth{RootEmail: rootEmail, Username: username, Password: password}
	if totpSecret != "" {
		auth.TOTP = &SecretTOTP{Secret: totpSecret}
	}
	return auth, "", true
}

// credentialsFromSection reads the IAM User keys from a credentials file
// section. It reports ok false when none of them are set.
func credentialsFromSection(section map[string]string) (*IAMUserAuth, bool) {
	rootEmail := section["root_email"]
	username := section["username"]
	password := section["password"]
	totpSecret := section["totp_secret"]
	if rootEmail == "" && username == "" && password == "" && totpSecret == "" {
		return nil, false
	}
	auth := &IAMUserAuth{RootEmail: rootEmail, Username: username, Password: password}
	if totpSecret != "" {
		auth.TOTP = &SecretTOTP{Secret: totpSecret}
	}
	return auth, true
}
