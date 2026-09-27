package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/volume"
)

// volumeJSON renders one GetVolume response for a volume matching the given
// fields, wrapped the way DetachVolume's and AttachVolume's own pre-write
// read decodes it.
func volumeJSON(uuid, status, serverID string, bootable bool) string {
	return fmt.Sprintf(`{"data":{"uuid":%q,"name":"data","status":%q,"serverId":%q,"bootable":%t}}`,
		uuid, status, serverID, bootable)
}

// TestGoldenVolumeAttachVolume checks attach-volume's exact output shape.
func TestGoldenVolumeAttachVolume(t *testing.T) {
	v := &volume.AttachVolumeOutput{Volume: exampleVolume(), Changed: true}
	checkGolden(t, "volume-attach-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-attach-volume.table.golden", "table", "", v)
}

// TestGoldenVolumeDetachVolume checks detach-volume's exact output shape.
func TestGoldenVolumeDetachVolume(t *testing.T) {
	v := &volume.DetachVolumeOutput{Volume: exampleVolume(), Changed: true}
	checkGolden(t, "volume-detach-volume.json.golden", "json", "", v)
	checkGolden(t, "volume-detach-volume.table.golden", "table", "", v)
}

// --- attach-volume ---

// TestVolumeAttachVolumeAlreadyAttachedSendsNoToggle checks that
// attach-volume on a volume already attached to --server-id sends only the
// one read and reports Changed: false.
func TestVolumeAttachVolumeAlreadyAttachedSendsNoToggle(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, volumeJSON("volume-1", "IN-USE", "server-1", false)),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "attach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1", "--query", "Changed",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("attach-volume: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read, no attach)", n)
	}
	if got := stdout.String(); got != "false\n" {
		t.Fatalf("--query Changed = %q, want %q", got, "false\n")
	}
}

// TestVolumeAttachVolumeTogglesAndConfirms drives an unattached volume
// through attach-volume end to end: one read, one PUT, and a settled read
// showing it IN-USE with the server attached, no --yes required (attach is
// a Write but not Destructive). The PUT flips the fixture's own state so
// the wait settles on its first poll, with no real sleep.
func TestVolumeAttachVolumeTogglesAndConfirms(t *testing.T) {
	status, serverID := "AVAILABLE", ""
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(volumeJSON("volume-1", status, serverID, false)))
		},
		"/v2/proj-1/volumes/volume-1/servers/server-1/attach": func(w http.ResponseWriter, _ *http.Request) {
			status, serverID = "IN-USE", "server-1"
			w.WriteHeader(http.StatusAccepted)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "attach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("attach-volume: %v (stderr=%s)", err, stderr.String())
	}
	var out struct {
		Volume  volume.Volume
		Changed bool
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout.String())
	}
	if !out.Changed || out.Volume.ServerID != "server-1" {
		t.Fatalf("out = %+v, want Changed true and ServerID server-1", out)
	}
}

// TestVolumeAttachVolumeReadOnlyRefusedWithZeroRequests checks the design's
// read-only rule for attach-volume.
func TestVolumeAttachVolumeReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{"--profile", "agent", "volume", "attach-volume", "--volume-id", "volume-1", "--server-id", "server-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", classify(err).Code, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// --- detach-volume ---

// TestVolumeDetachVolumeWithoutYesExitsWithZeroRequests checks the design's
// --yes rule: detach-volume is Write and Destructive.
func TestVolumeDetachVolumeWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestVolumeDetachVolumeNotAttachedSendsNoToggle checks that detach-volume
// on a volume not attached to --server-id sends only the one read and
// reports Changed: false.
func TestVolumeDetachVolumeNotAttachedSendsNoToggle(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, volumeJSON("volume-1", "AVAILABLE", "", false)),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1", "--query", "Changed",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("detach-volume: %v (stderr=%s)", err, stderr.String())
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (the one read, no detach)", n)
	}
}

// TestVolumeDetachVolumeRefusesBootVolumeWithZeroDetachRequests checks the
// design's boot-volume guard: a Bootable volume is refused with error code
// BootVolume, after the volume read and the server read the guard itself
// needs, and no PUT is ever sent. The server read still runs, since
// DetachVolume needs its own bootVolumeId to confirm the guard even when
// Volume.Bootable already says so.
func TestVolumeDetachVolumeRefusesBootVolumeWithZeroDetachRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, volumeJSON("volume-1", "IN-USE", "server-1", true)),
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, `{"data":{"status":"STOPPED","bootVolumeId":"volume-1"}}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a BootVolume refusal")
	}
	if got := classify(err).Code; got != "BootVolume" {
		t.Fatalf("Code = %q, want BootVolume (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (the volume read and the server read, no detach)", n)
	}
}

// TestVolumeDetachVolumeRefusesUnconfirmedBootVolumeWithZeroDetachRequests
// checks the design's fail-closed rule: a server read that reports no
// bootVolumeId at all is refused with error code BootVolume too, since a
// missing id cannot rule out this being the boot volume.
func TestVolumeDetachVolumeRefusesUnconfirmedBootVolumeWithZeroDetachRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, volumeJSON("volume-1", "IN-USE", "server-1", false)),
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, `{"data":{"status":"STOPPED"}}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a BootVolume refusal")
	}
	if got := classify(err).Code; got != "BootVolume" {
		t.Fatalf("Code = %q, want BootVolume (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (the volume read and the server read, no detach)", n)
	}
}

// TestVolumeDetachVolumeRefusesRunningServerWithoutAllowRunning checks the
// design's running-server guard: a server that reads back anything but
// STOPPED, without --allow-running, refuses with error code ServerRunning
// and no PUT is ever sent. bootVolumeId names a different volume, so the
// boot-volume guard passes and this test reaches the status guard.
func TestVolumeDetachVolumeRefusesRunningServerWithoutAllowRunning(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": jsonHandler(http.StatusOK, volumeJSON("volume-1", "IN-USE", "server-1", false)),
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, `{"data":{"status":"ACTIVE","bootVolumeId":"volume-boot"}}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a ServerRunning refusal")
	}
	if got := classify(err).Code; got != "ServerRunning" {
		t.Fatalf("Code = %q, want ServerRunning (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 2 {
		t.Fatalf("requestCount = %d, want 2 (volume read, server read, no detach)", n)
	}
}

// TestVolumeDetachVolumeAllowRunningSkipsOnlyTheStatusCheck checks that
// --allow-running still reads the server (the boot-volume guard needs
// bootVolumeId regardless) but skips only the STOPPED requirement, letting
// the detach through against an ACTIVE server, per the design's rule that
// AllowRunning is the caller's own consent to that one risk.
func TestVolumeDetachVolumeAllowRunningSkipsOnlyTheStatusCheck(t *testing.T) {
	status := "IN-USE"
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(volumeJSON("volume-1", status, "server-1", false)))
		},
		"/v2/proj-1/volumes/volume-1/servers/server-1/detach": func(w http.ResponseWriter, _ *http.Request) {
			status = "AVAILABLE"
			w.WriteHeader(http.StatusAccepted)
		},
		"/v2/proj-1/servers/server-1": jsonHandler(http.StatusOK, `{"data":{"status":"ACTIVE","bootVolumeId":"volume-boot"}}`),
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1", "--allow-running",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("detach-volume: %v (stderr=%s)", err, stderr.String())
	}
	if !containsChanged(stdout.String()) {
		t.Fatalf("stdout = %s, want Changed true", stdout.String())
	}
}

// containsChanged reports whether s, a rendered Output, shows Changed as
// true.
func containsChanged(s string) bool {
	var out struct{ Changed bool }
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return false
	}
	return out.Changed
}

// TestVolumeDetachVolumeReadOnlyRefusedWithZeroRequests checks the design's
// read-only rule for detach-volume.
func TestVolumeDetachVolumeReadOnlyRefusedWithZeroRequests(t *testing.T) {
	home := withCleanEnv(t)
	writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
	writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

	refuse := func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v2/proj-1/volumes/volume-1": refuse,
	})
	opts := newFakeServer(t, fixture.mux)
	withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root := newRootCmd(strings.NewReader(""), stdout, stderr)
	root.SetArgs([]string{
		"--profile", "agent", "--yes", "volume", "detach-volume",
		"--volume-id", "volume-1", "--server-id", "server-1",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a read-only refusal")
	}
	if classify(err).Code != "ReadOnly" {
		t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", classify(err).Code, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}
