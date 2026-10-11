package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadServiceAccountSources(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"options", "environment", "default", "named", "profile environment"} {
		t.Run(source, func(t *testing.T) {
			home := setupHome(t)
			opts := []Option{WithRegion("hcm-3")}
			switch source {
			case "options":
				opts = append(opts, WithServiceAccountCredentials("synthetic-client", "synthetic-secret", true))
			case "environment":
				t.Setenv("VNGCLOUD_CLIENT_ID", "synthetic-client")
				t.Setenv("VNGCLOUD_CLIENT_SECRET", "synthetic-secret")
			default:
				profile := "default"
				if source != "default" {
					profile = "dev"
				}
				writeFile(t, credentialsPath(home), "["+profile+"]\nclient_id = synthetic-client\nclient_secret = synthetic-secret\nroot_email = losing-incomplete-iam\naccess_token = ignored-file-token\n", 0o600)
				if source == "named" {
					opts = append(opts, WithProfile("dev"))
				}
				if source == "profile environment" {
					t.Setenv("VNGCLOUD_PROFILE", "dev")
				}
			}
			sends := 0
			rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
				sends++
				id, secret, ok := r.BasicAuth()
				if !ok || id != "synthetic-client" || secret != "synthetic-secret" {
					t.Error("wrong selected credentials")
				}
				return serviceAccountResponse(200, string(fixture)), nil
			})
			cacheDir := filepath.Join(home, "unused-cache")
			opts = append(opts, WithTokenCache(cacheDir), serviceAccountTestOption(rt, func() time.Time { return time.Unix(1700000000, 0) }))
			cfg, err := LoadConfig(context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			if sends != 0 {
				t.Fatal("loading exchanged credentials")
			}
			if _, err := os.Stat(cacheDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("loading touched cache: %v", err)
			}
			if ClientOf(cfg).UsesIAMUserLogin() {
				t.Fatal("service account has IAM provenance")
			}
			if err := cfg.Authenticate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if sends != 1 {
				t.Fatalf("exchanges = %d", sends)
			}
		})
	}
}

func TestLoadServiceAccountMethodAndSourceSelection(t *testing.T) {
	provider := &fakeCredentialsProvider{}
	iam := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "password"}
	for _, reverse := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			opts []Option
			want string
		}{
			{"provider", []Option{WithCredentialsProvider(provider), WithStaticToken("token"), WithServiceAccountCredentials("bad:id", "bad secret", true), WithIAMUser(&IAMUserAuth{})}, "provider"},
			{"static", []Option{WithStaticToken("token"), WithServiceAccountCredentials("bad:id", "bad secret", true), WithIAMUser(&IAMUserAuth{})}, "static"},
			{"service account", []Option{WithServiceAccountCredentials("option-id", "option-secret", true), WithIAMUser(&IAMUserAuth{})}, "service account"},
			{"IAM options reserve source", []Option{WithIAMUser(iam)}, "IAM"},
			{"nil options", []Option{WithCredentialsProvider(nil), WithStaticToken(""), WithServiceAccountCredentials("", "", false), WithIAMUser(nil)}, "environment"},
		} {
			t.Run(tc.name+map[bool]string{false: " forward", true: " reverse"}[reverse], func(t *testing.T) {
				setupHome(t)
				t.Setenv("VNGCLOUD_CLIENT_ID", "env-id")
				t.Setenv("VNGCLOUD_CLIENT_SECRET", "env-secret")
				settings := defaultClientConfig()
				opts := append([]Option(nil), tc.opts...)
				if reverse {
					for i, j := 0, len(opts)-1; i < j; i, j = i+1, j-1 {
						opts[i], opts[j] = opts[j], opts[i]
					}
				}
				for _, opt := range opts {
					opt.apply(&settings)
				}
				section := map[string]string{"client_id": "file-id", "client_secret": "file-secret"}
				if err := resolveCredentials(&settings, false, "default", section); err != nil {
					t.Fatal(err)
				}
				switch tc.want {
				case "provider":
					if settings.credentials != provider {
						t.Fatal("provider lost")
					}
				case "static":
					if settings.staticToken != "token" {
						t.Fatal("static lost")
					}
				case "service account":
					if settings.serviceAccount == nil || settings.serviceAccount.clientID != "option-id" {
						t.Fatal("option source lost")
					}
				case "IAM":
					if settings.iamUser != iam || settings.serviceAccount != nil {
						t.Fatal("mixed option and environment sources")
					}
				case "environment":
					if settings.serviceAccount == nil || settings.serviceAccount.clientID != "env-id" {
						t.Fatal("unset options reserved source")
					}
				}
				settings.region = "hcm-3"
				if _, err := buildClient(settings); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestLoadServiceAccountPartialAndShapeErrors(t *testing.T) {
	for _, source := range []string{"options", "environment", "profile"} {
		for index, fields := range [][2]string{{"", ""}, {"marker-id", ""}, {"", "marker-secret"}, {"bad:id", "marker-secret"}, {"marker-id", "bad secret"}, {"marker-id", "bad\u00a0secret"}, {"marker-id", "bad\x00secret"}, {strings.Repeat("x", 4097), "marker-secret"}, {"marker-id", strings.Repeat("y", 4097)}} {
			if (fields == [2]string{} && source != "options") || (source == "environment" && strings.ContainsRune(fields[1], 0)) {
				continue
			}
			t.Run(fmt.Sprintf("%s/%d", source, index), func(t *testing.T) {
				home := setupHome(t)
				opts := []Option{WithRegion("hcm-3")}
				switch source {
				case "options":
					opts = append(opts, WithServiceAccountCredentials(fields[0], fields[1], true))
					t.Setenv("VNGCLOUD_ACCESS_TOKEN", "fallback-token")
				case "environment":
					t.Setenv("VNGCLOUD_CLIENT_ID", fields[0])
					t.Setenv("VNGCLOUD_CLIENT_SECRET", fields[1])
					writeFile(t, credentialsPath(home), "[default]\nclient_id = file-id\nclient_secret = file-secret\n", 0o600)
				case "profile":
					writeFile(t, credentialsPath(home), "[default]\nclient_id = "+fields[0]+"\nclient_secret = "+fields[1]+"\nroot_email = root\nusername = user\npassword = fallback-password\n", 0o600)
				}
				cfg, err := LoadConfig(context.Background(), opts...)
				partial := fields[0] == "" || fields[1] == ""
				if !errors.Is(err, ErrInvalidConfig) || errors.Is(err, ErrNoCredentials) != partial {
					t.Fatalf("err = %v, partial = %v", err, partial)
				}
				if cfg.client != nil {
					t.Fatal("error returned usable config")
				}
				if !strings.Contains(err.Error(), source) {
					t.Fatalf("missing source label: %v", err)
				}
				for _, value := range fields {
					if value != "" && strings.Contains(err.Error(), value) {
						t.Fatal("credential in error")
					}
				}
			})
		}
	}
}

func TestLoadServiceAccountEnvironmentMethodSelection(t *testing.T) {
	for _, static := range []bool{false, true} {
		t.Run(map[bool]string{false: "service account", true: "static"}[static], func(t *testing.T) {
			setupHome(t)
			t.Setenv("VNGCLOUD_CLIENT_ID", "env-id")
			t.Setenv("VNGCLOUD_CLIENT_SECRET", "env-secret")
			t.Setenv("VNGCLOUD_USERNAME", "incomplete-iam")
			if static {
				t.Setenv("VNGCLOUD_ACCESS_TOKEN", "token")
				t.Setenv("VNGCLOUD_CLIENT_ID", "bad:id")
			}
			settings := defaultClientConfig()
			if err := resolveCredentials(&settings, false, "default", nil); err != nil {
				t.Fatal(err)
			}
			if static {
				if settings.staticToken != "token" {
					t.Fatal("static lost")
				}
			} else if settings.serviceAccount == nil || settings.serviceAccount.clientID != "env-id" {
				t.Fatal("service account lost")
			}
			if settings.iamUser != nil {
				t.Fatal("losing IAM selected")
			}
		})
	}
}

func TestLoadServiceAccountExplicitProfileAndCDN(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment profile", true: "explicit profile"}[explicit], func(t *testing.T) {
			home := setupHome(t)
			writeFile(t, configPath(home), "[profile dev]\nregion = file-region\nproject_id = file-project\n", 0o600)
			writeFile(t, credentialsPath(home), "[dev]\nclient_id = file-id\nclient_secret = file-secret\nvcdn_api_key = file-key\n", 0o600)
			t.Setenv("VNGCLOUD_PROFILE", "dev")
			t.Setenv("VNGCLOUD_REGION", "env-region")
			t.Setenv("VNGCLOUD_PROJECT_ID", "env-project")
			t.Setenv("VNGCLOUD_CLIENT_ID", "env-id")
			t.Setenv("VNGCLOUD_CLIENT_SECRET", "env-secret")
			t.Setenv("VNGCLOUD_VCDN_API_KEY", "env-key")
			wantID, wantKey, wantProject := "env-id", "env-key", "env-project"
			var opts []Option
			if explicit {
				opts = append(opts, WithProfile("dev"))
				wantID, wantKey, wantProject = "file-id", "file-key", "file-project"
				t.Setenv("VNGCLOUD_ACCESS_TOKEN", "ignored")
				t.Setenv("VNGCLOUD_PASSWORD", "ignored")
			}
			rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
				id, _, _ := r.BasicAuth()
				if id != wantID {
					t.Error("wrong identity")
				}
				return serviceAccountResponse(200, `{"access_token":"synthetic-token","token_type":"Bearer","expires_in":3600}`), nil
			})
			opts = append(opts, serviceAccountTestOption(rt, func() time.Time { return time.Unix(1700000000, 0) }))
			cfg, err := LoadConfig(context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}
			c := ClientOf(cfg)
			key, err := c.CDNAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if key != wantKey || cfg.Region() != "env-region" || c.projectID != wantProject {
				t.Fatal("profile leaked environment settings or lost independent CDN key")
			}
			if err := cfg.Authenticate(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLoadServiceAccountEmptyKeysAndMisplacedConfig(t *testing.T) {
	for _, key := range []string{"client_id", "client_secret"} {
		for _, value := range []string{"", "misplaced-marker"} {
			for _, named := range []bool{false, true} {
				t.Run(key+"/"+map[bool]string{false: "default", true: "named"}[named]+"/"+value, func(t *testing.T) {
					home := setupHome(t)
					section, profile := "default", "default"
					if named {
						section, profile = "profile dev", "dev"
					}
					writeFile(t, configPath(home), "["+section+"]\nregion = hcm-3\nunknown_key = kept\n"+key+" = "+value+"\n", 0o600)
					cfg, err := LoadConfig(context.Background(), WithProfile(profile), WithStaticToken("winner"))
					if value != "" {
						if !errors.Is(err, ErrInvalidConfig) || errors.Is(err, ErrNoCredentials) || cfg.client != nil {
							t.Fatalf("misplaced key: %v", err)
						}
						if !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "credentials file") || strings.Contains(err.Error(), value) {
							t.Fatalf("unsafe key error: %v", err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						if cfg.ProfileSetting("unknown_key") != "kept" {
							t.Fatal("unknown key lost")
						}
					}
				})
			}
		}
	}
	home := setupHome(t)
	writeFile(t, credentialsPath(home), "[default]\nclient_id =\nclient_secret =\nroot_email = root\nusername = user\npassword = password\n", 0o600)
	cfg, err := LoadConfig(context.Background(), WithRegion("hcm-3"))
	if err != nil {
		t.Fatal(err)
	}
	if !ClientOf(cfg).UsesIAMUserLogin() {
		t.Fatal("empty keys selected service account")
	}
}

func TestLoadServiceAccountEndedContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithDeadline(context.Background(), time.Time{})
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		defer cancel()
		_, err := LoadConfig(ctx, WithServiceAccountCredentials("", "", true), WithConfigFile("missing"))
		if err != ctx.Err() { //nolint:errorlint // LoadConfig must return the bare context sentinel.
			t.Fatalf("ended context wrapped: %v", err)
		}
	}
}

func TestLoadServiceAccountLosingSources(t *testing.T) {
	for _, option := range []Option{
		WithCredentialsProvider(&fakeCredentialsProvider{}), WithStaticToken("winner"),
		WithServiceAccountCredentials("option-id", "option-secret", true),
		WithIAMUser(&IAMUserAuth{RootEmail: "root", Username: "user", Password: "password"}),
	} {
		home := setupHome(t)
		t.Setenv("VNGCLOUD_CLIENT_ID", "bad:id")
		t.Setenv("VNGCLOUD_CLIENT_SECRET", "bad secret")
		writeFile(t, credentialsPath(home), "[default]\nclient_id = file:bad\nclient_secret =\n", 0o600)
		if _, err := LoadConfig(context.Background(), WithRegion("hcm-3"), option); err != nil {
			t.Fatal(err)
		}
	}
	home := setupHome(t)
	writeFile(t, credentialsPath(home), "[default]\nclient_id = file:bad\nclient_secret =\n", 0o600)
	t.Setenv("VNGCLOUD_CLIENT_ID", "env-id")
	t.Setenv("VNGCLOUD_CLIENT_SECRET", "env-secret")
	if _, err := LoadConfig(context.Background(), WithRegion("hcm-3")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadServiceAccountSelectedEmptyOptions(t *testing.T) {
	for _, option := range []Option{WithIAMUser(&IAMUserAuth{}), WithServiceAccountCredentials("", "", true)} {
		home := setupHome(t)
		t.Setenv("VNGCLOUD_ACCESS_TOKEN", "fallback")
		writeFile(t, credentialsPath(home), "[default]\nclient_id = file-id\nclient_secret = file-secret\n", 0o600)
		if _, err := LoadConfig(context.Background(), WithRegion("hcm-3"), option); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("empty option fell back: %v", err)
		}
	}
}
