//go:build livewrite

package livetest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/internal/testutil"
)

func TestIAMUserHarnessGates(t *testing.T) {
	for _, tc := range []struct {
		write, user string
		want        bool
	}{
		{"", "", false}, {"1", "", false}, {"", "1", false},
		{"true", "1", false}, {"1", "1", true},
	} {
		if iamUserGates(tc.write, tc.user) != tc.want {
			t.Fatal("gate mismatch")
		}
	}
}

func TestIAMUserHarnessLedger(t *testing.T) {
	for _, tc := range []struct {
		out  *iam.CreateUserOutput
		want string
	}{
		{nil, ""}, {&iam.CreateUserOutput{CandidateID: "candidate"}, ""},
		{&iam.CreateUserOutput{CreatedID: "owned", CandidateID: "other"}, "owned"},
	} {
		var ledger iamUserLedger
		ledger.record(tc.out)
		if ledger.ID != tc.want {
			t.Fatal("cleanup attribution mismatch")
		}
	}
}

func TestIAMUserHarnessSecretCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "password")
	if err := iamUserSaveSecret(path, vngcloud.Secret("Synthetic1@")); err != nil {
		t.Fatal("save failed")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("secret mode mismatch")
	}
	if err := iamUserSaveSecret(path, vngcloud.Secret("Replacement1@")); err == nil {
		t.Fatal("existing file replaced")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal("secret cleanup failed")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("secret remains")
	}
}

func iamUserGates(write, user string) bool { return write == "1" && user == "1" }

// Only the create response's ID grants cleanup authority. A filtered name
// match never enters this ledger, even after a successful get.
type iamUserLedger struct {
	Name            string
	ID              string
	CandidateID     string
	Attempted       bool
	Gone            bool
	StopCleanup     bool
	DeleteAttempted bool
}

func (l *iamUserLedger) record(out *iam.CreateUserOutput) {
	if out != nil {
		l.ID = out.CreatedID
		l.CandidateID = out.CandidateID
	}
}

func iamUserSaveSecret(path string, secret vngcloud.Secret) (result error) {
	if secret == "" {
		return errors.New("generated secret missing")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("secret exclusive open failed")
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			if removeErr := os.Remove(path); removeErr != nil {
				result = errors.New("secret persistence failed; partial secret may remain")
			}
		}
	}()
	if _, err = f.WriteString(secret.Reveal() + "\n"); err != nil {
		return errors.New("secret write failed")
	}
	if err = f.Sync(); err != nil {
		return errors.New("secret sync failed")
	}
	if err = f.Close(); err != nil {
		return errors.New("secret close failed")
	}
	ok = true
	return nil
}

type iamUserDeleteWatch struct {
	base    http.RoundTripper
	blocked atomic.Bool
	deletes atomic.Int64
}

func (w *iamUserDeleteWatch) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodDelete {
		w.deletes.Add(1)
		if w.blocked.Load() {
			return nil, errors.New("self-check DELETE blocked")
		}
	}
	return w.base.RoundTrip(req)
}

func iamUserSaveLedger(path string, ledger *iamUserLedger) error {
	data, err := json.Marshal(ledger)
	if err != nil {
		return errors.New("private ledger encoding failed")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.New("private ledger open failed")
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(data); err != nil {
		return errors.New("private ledger write failed")
	}
	if err = f.Sync(); err != nil {
		return errors.New("private ledger sync failed")
	}
	return f.Close()
}

// The fixed lock serializes this helper across processes. Other IAM writers
// still require manager serialization. A stale lock requires private inspection.
func iamUserLock(dir string) (func(), error) {
	path := filepath.Join(dir, "writer-lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, errors.New("IAM user writer lock unavailable")
	}
	return func() { _ = os.Remove(path) }, nil
}

func TestLiveWriteIAMUser(t *testing.T) {
	if !iamUserGates(os.Getenv("VNGCLOUD_LIVE_WRITE"), os.Getenv("VNGCLOUD_LIVE_IAM_USER")) {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_IAM_USER=1 after run authorization")
	}
	if envfile.Load(repoPath(".env")) != nil {
		t.Fatal("environment load failed")
	}
	dir := repoPath("examples/basic/output/iam-user-live")
	if os.MkdirAll(dir, 0o700) != nil {
		t.Fatal("private ledger directory failed")
	}
	unlock, err := iamUserLock(dir)
	if err != nil {
		t.Fatal("IAM user writer lock unavailable; inspect private leftovers")
	}
	t.Cleanup(unlock)
	suffix := make([]byte, 4)
	if _, err = rand.Read(suffix); err != nil {
		t.Fatal("unique name generation failed")
	}
	ledger := &iamUserLedger{Name: "vngcloud-live-" + hex.EncodeToString(suffix)}
	report := filepath.Join(dir, ledger.Name+".json")
	save := func() bool { return iamUserSaveLedger(report, ledger) == nil }
	if !save() {
		t.Fatal("private ledger persistence failed")
	}
	watch := &iamUserDeleteWatch{base: http.DefaultTransport}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
		vngcloud.WithHTTPClient(&http.Client{Transport: watch, Timeout: 30 * time.Second}),
	)
	if err != nil {
		t.Fatal("configuration failed")
	}
	client := iam.New(cfg)
	caller, err := client.GetCallerIdentity(ctx, nil)
	if err != nil || caller.UserType != "iam-user" || caller.UserID == "" {
		t.Fatal("IAM user caller required")
	}
	watch.blocked.Store(true)
	_, selfErr := client.DeleteUser(ctx, &iam.DeleteUserInput{UserID: caller.UserID})
	watch.blocked.Store(false)
	if !errors.Is(selfErr, iam.ErrSelfChange) || watch.deletes.Load() != 0 {
		t.Fatal("self refusal failed")
	}
	t.Log("self refusal=true DELETE count=0")
	pre, err := client.ListUsers(ctx, &iam.ListUsersInput{Name: ledger.Name, Size: 1000})
	if err != nil || len(pre.Items) != 0 {
		t.Fatal("absent-name precheck failed")
	}
	// Register cleanup before create. Never search for an ID during cleanup.
	t.Cleanup(func() {
		defer func() {
			if !save() {
				t.Error("private ledger persistence failed")
			}
		}()
		if ledger.Gone || !ledger.Attempted {
			return
		}
		if ledger.ID == "" || ledger.StopCleanup || ledger.DeleteAttempted {
			t.Error("leftover unresolved; inspect private IAM user ledger; no automatic cleanup")
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if !iamUserDeleteOwned(cleanupCtx, client, ledger) {
			t.Error("owned user cleanup unresolved; inspect private IAM user ledger")
		}
	})
	ledger.Attempted = true
	if !save() {
		ledger.StopCleanup = true
		t.Fatal("private ledger persistence failed")
	}
	created, createErr := client.CreateUser(ctx, &iam.CreateUserInput{Username: ledger.Name})
	ledger.record(created)
	if !save() {
		ledger.StopCleanup = true
		t.Fatal("private ledger persistence failed")
	}
	if created != nil && created.Password != "" {
		secretPath := filepath.Join(t.TempDir(), "password")
		// No API calls follow a secret-file failure, including deferred cleanup.
		if iamUserSaveSecret(secretPath, created.Password) != nil {
			ledger.StopCleanup = true
			t.Fatal("secret persistence failed; inspect private IAM user ledger")
		}
		created.Password = ""
		if os.Remove(secretPath) != nil {
			ledger.StopCleanup = true
			t.Fatal("temporary secret removal failed")
		}
		t.Log("secret persisted=true removed=true")
	} else {
		ledger.StopCleanup = true
		t.Fatal("generated secret missing")
	}
	if createErr != nil || ledger.ID == "" {
		t.Fatal("create unconfirmed; inspect private IAM user ledger")
	}
	got, err := client.GetUser(ctx, &iam.GetUserInput{UserID: ledger.ID})
	if err != nil || !strings.EqualFold(got.User.ID, ledger.ID) || got.User.Username != ledger.Name || got.User.From != "iam" {
		ledger.StopCleanup = true
		t.Fatal("created user identity unconfirmed; inspect private IAM user ledger")
	}
	listed, err := client.ListUsers(ctx, &iam.ListUsersInput{Name: ledger.Name, Size: 1000})
	if err != nil {
		t.Fatal("filtered list failed")
	}
	matches := 0
	for _, row := range listed.Items {
		if row.Username == ledger.Name {
			if !strings.EqualFold(row.ID, ledger.ID) {
				ledger.StopCleanup = true
				t.Fatal("filtered identity mismatch")
			}
			matches++
		}
	}
	if matches != 1 {
		ledger.StopCleanup = true
		t.Fatal("filtered identity ambiguous")
	}
	if !iamUserDeleteOwned(ctx, client, ledger) {
		t.Fatal("guarded delete unresolved; inspect private IAM user ledger")
	}
	t.Log("create=true get=true filtered match count=1 delete=true absent=true")
}

func iamUserDeleteOwned(ctx context.Context, client *iam.Client, ledger *iamUserLedger) bool {
	if ledger.ID == "" || ledger.StopCleanup || ledger.DeleteAttempted {
		return false
	}
	got, err := client.GetUser(ctx, &iam.GetUserInput{UserID: ledger.ID})
	if vngcloud.IsNotFound(err) {
		ledger.Gone = true
		return true
	}
	if err != nil || !strings.EqualFold(got.User.ID, ledger.ID) || got.User.Username != ledger.Name || got.User.From != "iam" {
		return false
	}
	ledger.DeleteAttempted = true
	if _, err = client.DeleteUser(ctx, &iam.DeleteUserInput{UserID: ledger.ID}); err != nil {
		return false
	}
	_, err = client.GetUser(ctx, &iam.GetUserInput{UserID: ledger.ID})
	ledger.Gone = vngcloud.IsNotFound(err)
	return ledger.Gone
}

func TestIAMUserHarnessNoSweep(t *testing.T) {
	requests := 0
	client := iam.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Error("unattributed cleanup made a request")
		w.WriteHeader(http.StatusInternalServerError)
	})))
	for _, ledger := range []*iamUserLedger{
		{Name: "vngcloud-live-12345678", CandidateID: "candidate"},
		{ID: "owned", StopCleanup: true}, {ID: "owned", DeleteAttempted: true},
	} {
		if iamUserDeleteOwned(context.Background(), client, ledger) {
			t.Fatal("unattributed cleanup accepted")
		}
	}
	if requests != 0 {
		t.Fatal("cleanup made requests")
	}
}

func TestIAMUserHarnessLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := iamUserLock(dir)
	if err != nil {
		t.Fatal("lock failed")
	}
	if _, err = iamUserLock(dir); err == nil {
		t.Fatal("concurrent writer accepted")
	}
	unlock()
	unlock, err = iamUserLock(dir)
	if err != nil {
		t.Fatal("lock not released")
	}
	unlock()
}

func TestIAMUserHarnessOwnedDelete(t *testing.T) {
	deletes := 0
	gone := false
	client := iam.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			if !strings.HasSuffix(r.URL.Path, "/iam-users/owned") {
				t.Error("unowned delete")
			}
			deletes++
			gone = true
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/auth/userinfo"):
			_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
		case strings.HasSuffix(r.URL.Path, "/policies"), strings.HasSuffix(r.URL.Path, "/groups"):
			t.Error("cleanup queried attachments")
			w.WriteHeader(http.StatusForbidden)
		case strings.HasSuffix(r.URL.Path, "/iam-users/owned"):
			if gone {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"id":"owned","username":"vngcloud-live-12345678","from":"iam"}`))
		default:
			t.Error("unexpected inventory or prefix search")
			w.WriteHeader(http.StatusBadRequest)
		}
	})))
	ledger := &iamUserLedger{Name: "vngcloud-live-12345678", ID: "owned"}
	if !iamUserDeleteOwned(context.Background(), client, ledger) || !ledger.Gone || deletes != 1 {
		t.Fatal("owned cleanup failed")
	}
}

func TestIAMUserHarnessDeleteFailureLeftover(t *testing.T) {
	for _, status := range []int{http.StatusConflict, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			deletes := 0
			client := iam.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/iam-users/owned"):
					deletes++
					w.WriteHeader(status)
				case strings.HasSuffix(r.URL.Path, "/auth/userinfo"):
					_, _ = w.Write([]byte(`{"userId":"caller","userType":"iam-user"}`))
				case strings.HasSuffix(r.URL.Path, "/iam-users/owned"):
					_, _ = w.Write([]byte(`{"id":"owned","username":"created-name","from":"iam"}`))
				default:
					t.Error("cleanup queried attachments or searched by name")
					w.WriteHeader(http.StatusForbidden)
				}
			})))
			ledger := &iamUserLedger{ID: "owned", Name: "created-name"}
			if iamUserDeleteOwned(context.Background(), client, ledger) || ledger.Gone || !ledger.DeleteAttempted || deletes != 1 {
				t.Fatal("delete failure not retained as leftover")
			}
			if iamUserDeleteOwned(context.Background(), client, ledger) || deletes != 1 {
				t.Fatal("cleanup retried leftover")
			}
		})
	}
}
