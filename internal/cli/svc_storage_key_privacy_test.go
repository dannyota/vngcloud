package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageCreateS3KeyAttachWithholdsIssuedSecret(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		t.Run(format, func(t *testing.T) {
			s := defaultAttachSetup()
			s.attach = `{"success":false,"code":"` + keyFixtureToken + `","errorMsg":"` + keyFixtureToken + `"}`
			routes, _ := attachRoutes(t, s)
			r := runStorage(t, routes, "--output", format, "--project-id", "proj-s1", "storage", "create-s3-key", "--service-account-id", "sa-1", "--secret-file", filepath.Join(t.TempDir(), "credentials"))
			if r.err == nil {
				t.Fatal("attach refusal accepted")
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v %s %s", r.err, r.err, r.err, r.stdout, r.stderr), keyFixtureToken) {
				t.Fatal("issued secret escaped from dependent attach")
			}
		})
	}
}
