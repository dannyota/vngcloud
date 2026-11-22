package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigureCredentialsMissingConfigParent(t *testing.T) {
	home := withCleanEnv(t)
	t.Setenv(envConfigFileVar, filepath.Join(home, "missing", "config"))
	credentials := filepath.Join(home, "credentials")
	t.Setenv(envCredsFileVar, credentials)
	if _, err := runConfigure(t, "", []string{"configure", "set", "username", "value"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credentials); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "missing")); !os.IsNotExist(err) {
		t.Fatalf("config parent exists: %v", err)
	}
}

func TestConfigureCaseVariantSameFile(t *testing.T) {
	home := withCleanEnv(t)
	path := filepath.Join(home, "book")
	if err := os.WriteFile(path, []byte("[default]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(home, "BOOK")
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(upper)
	if os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(first, second) {
		t.Skip("case variants are distinct files")
	}
	t.Setenv(envConfigFileVar, path)
	t.Setenv(envCredsFileVar, upper)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	flags := &globalFlags{}
	if err := applyConfigureValues(ctx, flags, map[string]string{"region": "hcm-3", "username": "value"}); err != nil {
		t.Fatal(err)
	}
}
