package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	attachPathS3  = keyPathS3 + "/attach"
	detachPathS3  = keyPathS3 + "/detach"
	detailsPathS3 = "/internal/v1/users/details"

	s3AttachOKBody       = `{"code":200,"success":true}`
	s3AttachRepeatBody   = `{"code":114,"success":false,"errorMsg":"This S3 key is already attached with this service account"}`
	s3AttachElsewhere    = `{"code":114,"success":false,"errorMsg":"This S3 key is already attached with another service account"}`
	s3DetachRepeatBody   = `{"code":114,"success":false,"errorMsg":"This S3 key is not attached to any service account."}`
	s3PrincipalBody      = `{"code":200,"success":true,"data":{"subUserId":"acct-user:sa-app"}}`
	s3PrincipalIAMBody   = `{"code":200,"success":true,"data":{"subUserId":"acct-user:iam-user"}}`
	s3PrincipalARN       = "arn:aws:iam:::user/acct-user:sa-app"
	s3AttachBodyWanted   = `PUT /attach {"projectId":"proj-s1","serviceAccountId":"sa-1"}`
	s3DetachBodyWanted   = `PUT /detach {"projectId":"proj-s1"}`
	s3CreateCallWanted   = `POST {"projectId":"proj-s1"}`
	s3DeleteCallWanted   = `DELETE {"projectId":"proj-s1"}`
	s3EnsureQueryWanted  = "GET generated=true&iam_account_id=sa-sa-1&project_id=proj-s1"
	attachFixtureAbsent  = "the attach ran after the secret file was written"
	attachFixtureWritten = "a secret file exists after a failed attach"
)

// attachRoutes extends keyRoutes with the attach, detach, and sub-user
// routes. attachStatus and attachBody answer the attach PUT; onAttach, when
// set, runs inside the handler before it answers.
type attachSetup struct {
	create, del, attach, detach, details string
	attachStatus                         int
	onAttach                             func()
}

func attachRoutes(t *testing.T, s attachSetup) (map[string]func(http.ResponseWriter, *http.Request), *keyRecorder) {
	t.Helper()
	routes, rec := keyRoutes(t, "", s.create, s.del)
	body := func(r *http.Request) string {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		return string(b[:n])
	}
	status := s.attachStatus
	if status == 0 {
		status = http.StatusOK
	}
	routes[attachPathS3] = func(w http.ResponseWriter, r *http.Request) {
		rec.record(r, "/attach "+body(r))
		if s.onAttach != nil {
			s.onAttach()
		}
		jsonHandler(status, s.attach)(w, r)
	}
	routes[detachPathS3] = func(w http.ResponseWriter, r *http.Request) {
		rec.record(r, "/detach "+body(r))
		jsonHandler(http.StatusOK, s.detach)(w, r)
	}
	routes[detailsPathS3] = func(w http.ResponseWriter, r *http.Request) {
		rec.record(r, "")
		jsonHandler(http.StatusOK, s.details)(w, r)
	}
	return routes, rec
}

func defaultAttachSetup() attachSetup {
	return attachSetup{
		create: s3KeyCreateBody, del: s3KeyDeleteBody, attach: s3AttachOKBody,
		detach: s3AttachOKBody, details: s3PrincipalBody,
	}
}

func TestStorageAttachS3Key(t *testing.T) {
	routes, rec := attachRoutes(t, defaultAttachSetup())
	r := runStorage(t, routes, "--yes", "--project-id", "proj-s1", "storage", "attach-s3-key",
		"--user-key-id", "key-1", "--service-account-id", "sa-1")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if got := rec.seen(); len(got) != 1 || got[0] != s3AttachBodyWanted {
		t.Fatalf("calls = %q, want %q", got, s3AttachBodyWanted)
	}
}

func TestStorageDetachS3Key(t *testing.T) {
	routes, rec := attachRoutes(t, defaultAttachSetup())
	r := runStorage(t, routes, "--yes", "--project-id", "proj-s1", "storage", "detach-s3-key", "--user-key-id", "key-1")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if got := rec.seen(); len(got) != 1 || got[0] != s3DetachBodyWanted {
		t.Fatalf("calls = %q, want %q", got, s3DetachBodyWanted)
	}
}

func TestStorageAttachDetachRequireYes(t *testing.T) {
	for name, args := range map[string][]string{
		"attach-s3-key": {"attach-s3-key", "--user-key-id", "key-1", "--service-account-id", "sa-1"},
		"detach-s3-key": {"detach-s3-key", "--user-key-id", "key-1"},
	} {
		t.Run(name, func(t *testing.T) {
			routes, rec := attachRoutes(t, defaultAttachSetup())
			r := runStorage(t, routes, append([]string{"--project-id", "proj-s1", "storage"}, args...)...)
			if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--yes") {
				t.Fatalf("err = %v, exit = %d, want exit 2 naming --yes", r.err, exitCode(r.err))
			}
			if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
				t.Fatalf("calls = %q, want none", rec.seen())
			}
		})
	}
}

func TestStorageEnsureServiceAccountPrincipalNeedsNoYes(t *testing.T) {
	routes, rec := attachRoutes(t, defaultAttachSetup())
	r := runStorage(t, routes, "--debug", "--project-id", "proj-s1", "storage",
		"ensure-service-account-principal", "--service-account-id", "sa-1")
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	for _, want := range []string{`"SubUserID": "acct-user:sa-app"`, `"PrincipalARN": "` + s3PrincipalARN + `"`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout = %s, want %s", r.stdout, want)
		}
	}
	if got := rec.seen(); len(got) != 1 || got[0] != s3EnsureQueryWanted {
		t.Fatalf("calls = %q, want %q", got, s3EnsureQueryWanted)
	}
}

func TestStorageEnsureServiceAccountPrincipalRefusesAnIAMPrincipal(t *testing.T) {
	setup := defaultAttachSetup()
	setup.details = s3PrincipalIAMBody
	routes, _ := attachRoutes(t, setup)
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage",
		"ensure-service-account-principal", "--service-account-id", "sa-1")
	if r.err == nil || exitCode(r.err) != 1 || classify(r.err).Code != "NotServiceAccountPrincipal" {
		t.Fatalf("err = %v, exit = %d, want NotServiceAccountPrincipal exit 1", r.err, exitCode(r.err))
	}
	if strings.Contains(r.stdout, "iam-user") {
		t.Fatalf("stdout printed the IAM principal: %s", r.stdout)
	}
}

func TestStorageAttachDetachCode114PrintsTheServerMessage(t *testing.T) {
	cases := map[string]struct {
		args []string
		set  func(*attachSetup)
		want string
	}{
		"repeat attach": {
			[]string{"attach-s3-key", "--user-key-id", "key-1", "--service-account-id", "sa-1"},
			func(s *attachSetup) { s.attach = s3AttachRepeatBody },
			"already attached with this service account",
		},
		"attached elsewhere": {
			[]string{"attach-s3-key", "--user-key-id", "key-1", "--service-account-id", "sa-1"},
			func(s *attachSetup) { s.attach = s3AttachElsewhere },
			"already attached with another service account",
		},
		"unattached detach": {
			[]string{"detach-s3-key", "--user-key-id", "key-1"},
			func(s *attachSetup) { s.detach = s3DetachRepeatBody },
			"not attached to any service account",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setup := defaultAttachSetup()
			tc.set(&setup)
			routes, _ := attachRoutes(t, setup)
			r := runStorage(t, routes, append([]string{"--yes", "--project-id", "proj-s1", "storage"}, tc.args...)...)
			if r.err == nil || exitCode(r.err) != 1 {
				t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
			}
			if env := classify(r.err); env.Code != "114" || !strings.Contains(env.Message, tc.want) {
				t.Fatalf("envelope = %+v, want code 114 with %q", env, tc.want)
			}
		})
	}
}

func TestStorageAttachWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	for name, args := range map[string][]string{
		"attach-s3-key":                      {"attach-s3-key", "--user-key-id", "key-1", "--service-account-id", "sa-1"},
		"detach-s3-key":                      {"detach-s3-key", "--user-key-id", "key-1"},
		"ensure-service-account-principal":   {"ensure-service-account-principal", "--service-account-id", "sa-1"},
		"create-s3-key --service-account-id": {"create-s3-key", "--service-account-id", "sa-1", "--secret-file", path},
	} {
		t.Run(name, func(t *testing.T) {
			routes, rec := attachRoutes(t, defaultAttachSetup())
			r := runStorage(t, routes, append([]string{"--read-only", "--yes", "--project-id", "proj-s1", "storage"}, args...)...)
			if r.err == nil || exitCode(r.err) != 2 || classify(r.err).Code != "ReadOnly" {
				t.Fatalf("err = %v, exit = %d, want ReadOnly exit 2", r.err, exitCode(r.err))
			}
			if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
				t.Fatalf("calls = %q, want none", rec.seen())
			}
		})
	}
}

func TestStorageAttachCommandsMissingProjectIDStopBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"--yes", "storage", "attach-s3-key", "--user-key-id", "key-1", "--service-account-id", "sa-1"},
		{"--yes", "storage", "detach-s3-key", "--user-key-id", "key-1"},
		{"storage", "ensure-service-account-principal", "--service-account-id", "sa-1"},
	} {
		routes, rec := attachRoutes(t, defaultAttachSetup())
		r := runStorage(t, routes, args...)
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--project-id") {
			t.Fatalf("%v: err = %v, exit = %d, want exit 2 naming --project-id", args, r.err, exitCode(r.err))
		}
		if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
			t.Errorf("%v: calls = %q, want none", args, rec.seen())
		}
	}
}

func TestStorageCreateS3KeyWithServiceAccountAttachesBeforeWritingTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials")
	setup := defaultAttachSetup()
	setup.onAttach = func() {
		if _, err := os.Lstat(path); err == nil {
			t.Error(attachFixtureAbsent)
		}
	}
	routes, rec := attachRoutes(t, setup)
	r := runStorage(t, routes, "--debug", "--project-id", "proj-s1", "storage", "create-s3-key",
		"--service-account-id", "sa-1", "--secret-file", path)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if want := []string{s3CreateCallWanted, s3AttachBodyWanted}; strings.Join(rec.seen(), "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", rec.seen(), want)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != s3CredentialsFile {
		t.Fatalf("file = %q, err = %v, want %q", data, err, s3CredentialsFile)
	}
	for _, want := range []string{`"ServiceAccountID": "sa-1"`, `"UserKeyID": "key-1"`, `"SecretKey": "[redacted]"`, `"SecretFile": "` + path + `"`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("stdout = %s, want %s", r.stdout, want)
		}
	}
	noLeak(t, r)
}

func TestStorageCreateS3KeyWithoutServiceAccountSendsNoAttach(t *testing.T) {
	routes, rec := attachRoutes(t, defaultAttachSetup())
	path := filepath.Join(t.TempDir(), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key", "--secret-file", path)
	if r.err != nil {
		t.Fatalf("execute: %v (%s)", r.err, r.stderr)
	}
	if got := rec.seen(); len(got) != 1 || got[0] != s3CreateCallWanted {
		t.Fatalf("calls = %q, want only the create", got)
	}
	if !strings.Contains(r.stdout, `"ServiceAccountID": ""`) {
		t.Fatalf("stdout = %s, want an empty ServiceAccountID", r.stdout)
	}
}

func TestStorageCreateS3KeyRefusesABadServiceAccountIDBeforeAnyRequest(t *testing.T) {
	for _, id := range []string{"", "a/b", "..", "sa 1"} {
		routes, rec := attachRoutes(t, defaultAttachSetup())
		path := filepath.Join(t.TempDir(), "credentials")
		r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key",
			"--service-account-id", id, "--secret-file", path)
		if r.err == nil || exitCode(r.err) != 2 || !strings.Contains(r.err.Error(), "--service-account-id") {
			t.Fatalf("%q: err = %v, exit = %d, want exit 2 naming --service-account-id", id, r.err, exitCode(r.err))
		}
		if r.fixture.requestCount() != 0 || len(rec.seen()) != 0 {
			t.Errorf("%q: calls = %q, want none", id, rec.seen())
		}
	}
}

func TestStorageCreateS3KeyFailedAttachDeletesTheKeyAndWritesNoFile(t *testing.T) {
	cases := map[string]struct {
		set      func(*attachSetup)
		wantCode string
	}{
		"code 114":        {func(s *attachSetup) { s.attach = s3AttachElsewhere }, "114"},
		"ambiguous 502":   {func(s *attachSetup) { s.attach, s.attachStatus = `{"code":502}`, http.StatusBadGateway }, ""},
		"unknown account": {func(s *attachSetup) { s.attach = `{"code":114,"success":false,"errorMsg":"StatusCode=404"}` }, "114"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			setup := defaultAttachSetup()
			tc.set(&setup)
			routes, rec := attachRoutes(t, setup)
			path := filepath.Join(t.TempDir(), "credentials")
			r := runStorage(t, routes, "--debug", "--project-id", "proj-s1", "storage", "create-s3-key",
				"--service-account-id", "sa-1", "--secret-file", path)
			if r.err == nil || exitCode(r.err) != 1 {
				t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
			}
			if tc.wantCode != "" && classify(r.err).Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", classify(r.err).Code, tc.wantCode)
			}
			if msg := r.err.Error(); !strings.Contains(msg, "key-1") || !strings.Contains(msg, "was deleted") {
				t.Fatalf("error = %q, want the key id and that it was deleted", msg)
			}
			if want := []string{s3CreateCallWanted, s3AttachBodyWanted, s3DeleteCallWanted}; strings.Join(rec.seen(), "|") != strings.Join(want, "|") {
				t.Fatalf("calls = %q, want %q", rec.seen(), want)
			}
			if _, err := os.Lstat(path); err == nil {
				t.Fatal(attachFixtureWritten)
			}
			if r.stdout != "" {
				t.Fatalf("stdout = %q, want empty", r.stdout)
			}
			noLeak(t, r)
		})
	}
}

func TestStorageCreateS3KeyFailedAttachNamesTheKeyWhenTheDeleteFails(t *testing.T) {
	setup := defaultAttachSetup()
	setup.attach = s3AttachElsewhere
	setup.del = `{"code":500,"success":false,"errorMsg":"boom <secret>"}`
	routes, rec := attachRoutes(t, setup)
	path := filepath.Join(t.TempDir(), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key",
		"--service-account-id", "sa-1", "--secret-file", path)
	if r.err == nil || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
	}
	msg := r.err.Error()
	if !strings.Contains(msg, "key-1") || !strings.Contains(msg, "could not be deleted") {
		t.Fatalf("error = %q, want the key id and the failed delete", msg)
	}
	if rec.deletes() != 1 {
		t.Fatalf("calls = %q, want one DELETE", rec.seen())
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal(attachFixtureWritten)
	}
	noLeak(t, r)
}

func TestStorageCreateS3KeyFailedAttachCleanupTreatsCode114AsDeleted(t *testing.T) {
	setup := defaultAttachSetup()
	setup.attach = s3AttachElsewhere
	setup.del = s3KeyRepeatDeleteBody
	routes, rec := attachRoutes(t, setup)
	path := filepath.Join(t.TempDir(), "credentials")
	r := runStorage(t, routes, "--project-id", "proj-s1", "storage", "create-s3-key",
		"--service-account-id", "sa-1", "--secret-file", path)
	if r.err == nil || exitCode(r.err) != 1 {
		t.Fatalf("err = %v, exit = %d, want exit 1", r.err, exitCode(r.err))
	}
	if msg := r.err.Error(); strings.Contains(msg, "could not be deleted") || !strings.Contains(msg, "was deleted") {
		t.Fatalf("error = %q, want a deleted key", msg)
	}
	if rec.deletes() != 1 {
		t.Fatalf("calls = %q, want one DELETE", rec.seen())
	}
}
