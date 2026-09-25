package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenDocsIsDeterministic(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	if err := runGenDocs(dir1); err != nil {
		t.Fatalf("runGenDocs(dir1): %v", err)
	}
	if err := runGenDocs(dir2); err != nil {
		t.Fatalf("runGenDocs(dir2): %v", err)
	}
	entries, err := os.ReadDir(dir1)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no files were generated")
	}
	for _, entry := range entries {
		b1, err := os.ReadFile(filepath.Join(dir1, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s (run 1): %v", entry.Name(), err)
		}
		b2, err := os.ReadFile(filepath.Join(dir2, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s (run 2): %v", entry.Name(), err)
		}
		if !bytes.Equal(b1, b2) {
			t.Errorf("%s differs between two runs", entry.Name())
		}
	}
}

func TestGenDocsWritesExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	for _, name := range []string{"CLI.md", "CLI-Billing.md", "CLI-Pricing.md", "CLI-Compute.md", "CLI-Network.md", "CLI-DNS.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestGenDocsEveryOpAppears(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	check := func(file string, names []string) {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", file, err)
		}
		for _, name := range names {
			if !strings.Contains(string(data), "## "+name) {
				t.Errorf("%s is missing operation %q", file, name)
			}
		}
	}
	check("CLI-Billing.md", opNames(billingOps))
	check("CLI-Pricing.md", opNames(pricingOps))
	check("CLI-Compute.md", opNames(computeOps))
	check("CLI-Network.md", opNames(networkOps))
	check("CLI-DNS.md", opNames(dnsOps))
}

func TestGenDocsStartsWithTheGeneratedMarker(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		if !strings.HasPrefix(string(data), genDocsMarker) {
			t.Errorf("%s does not start with the generated marker", entry.Name())
		}
	}
}

func TestGenDocsNoOSDependentContent(t *testing.T) {
	dir := t.TempDir()
	if err := runGenDocs(dir); err != nil {
		t.Fatalf("runGenDocs: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory to check against")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), err)
		}
		if strings.Contains(string(data), home) {
			t.Errorf("%s contains the local home directory path", entry.Name())
		}
	}
}

func TestGenDocsCommandIsHidden(t *testing.T) {
	withCleanEnv(t)
	out, err := runConfigure(t, "", []string{"--help"})
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if strings.Contains(out, "gen-docs") {
		t.Fatalf("gen-docs should be hidden from help output:\n%s", out)
	}
}

func TestGenDocsCommandRuns(t *testing.T) {
	withCleanEnv(t)
	dir := t.TempDir()
	if _, err := runConfigure(t, "", []string{"gen-docs", dir}); err != nil {
		t.Fatalf("gen-docs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLI.md")); err != nil {
		t.Fatalf("CLI.md was not written: %v", err)
	}
}
