package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\n\nexport FOO=bar\nQUOTED=\"with spaces\"\nEMPTY=\nPRESET=from-file\nNOEQUALS\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRESET", "from-env")

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := os.Getenv("FOO"); got != "bar" {
		t.Fatalf("FOO = %q", got)
	}
	if got := os.Getenv("QUOTED"); got != "with spaces" {
		t.Fatalf("QUOTED = %q", got)
	}
	if got := os.Getenv("PRESET"); got != "from-env" {
		t.Fatalf("existing environment must win, PRESET = %q", got)
	}
}

func TestLoadMissingFileIsNoError(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "absent.env")); err != nil {
		t.Fatalf("Load() on missing file = %v", err)
	}
}
