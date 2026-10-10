//go:build livewrite

package livetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
	"danny.vn/vngcloud/internal/envfile"
)

// TestLiveWriteCDN drives one Web Accelerator CDN through every write: it
// disables, enables, updates, purges, and deletes it. The API cannot create a
// CDN (it answers "Create CDN failed."), so the owner creates it in the vCDN
// Portal first, as a name that starts vngcloud-live- or vngcloud-probe-. The
// test takes the first such CDN and skips when there is none. It needs
// VNGCLOUD_LIVE_WRITE=1, VNGCLOUD_LIVE_CDN=1, and VNGCLOUD_VCDN_API_KEY, and
// takes about 20 minutes of waits, so run it with -timeout 60m.
//
// Deleting the CDN is the last step, and the test waits for it to vanish, so
// a CDN with no traffic costs nothing and the account holds none afterwards. A failed run leaves the CDN in
// place and logs it as a leftover rather than guess which state is safe to
// delete. The test logs statuses, codes, and counts only.
func TestLiveWriteCDN(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_CDN") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 and VNGCLOUD_LIVE_CDN=1 to run the live vCDN write test")
	}
	if err := envfile.Load(repoPath(".env")); err != nil {
		t.Fatalf("load .env: %v", err)
	}
	key := os.Getenv("VNGCLOUD_VCDN_API_KEY")
	if key == "" {
		t.Skip("set VNGCLOUD_VCDN_API_KEY to run the live vCDN write test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	var purgeCapture *vngcloud.ResponseCapture
	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithStaticToken("unused"),
		vngcloud.WithCDNAPIKey(key),
		vngcloud.WithResponseCapture(func(captured vngcloud.ResponseCapture) {
			if captured.Operation != "cdn.PurgePaths" {
				return
			}
			copy := captured
			copy.Body = append([]byte(nil), captured.Body...)
			purgeCapture = &copy
		}),
	)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	client := cdn.New(cfg)

	list, err := client.ListWebAccelerators(ctx, nil)
	if err != nil {
		t.Fatalf("ListWebAccelerators: %v", err)
	}
	var target *cdn.WebAcceleratorSummary
	for i, item := range list.Items {
		if strings.HasPrefix(item.DomainName, "vngcloud-live-") || strings.HasPrefix(item.DomainName, "vngcloud-probe-") {
			target = &list.Items[i]
			break
		}
	}
	t.Logf("web accelerators: %d", len(list.Items))
	if target == nil {
		t.Skip("no CDN named vngcloud-live-* or vngcloud-probe-*: create one in the vCDN Portal first")
	}
	id := target.CDNID
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			t.Logf("LEFTOVER: the test CDN still exists (status %s); delete it with the CLI or the portal", cdn.StatusName(liveCDNStatus(ctx, client, id)))
		}
	})

	// Start from ACTIVE: wait out a CDN that is still deploying, and enable
	// one that is disabled.
	wa := liveCDNSettle(ctx, t, client, id, cdn.StatusActive)
	if wa.Status == cdn.StatusDisabled {
		out, err := client.EnableWebAccelerator(ctx, &cdn.EnableWebAcceleratorInput{CDNID: id})
		if err != nil {
			t.Fatalf("EnableWebAccelerator (setup): %v", err)
		}
		wa = out.WebAccelerator
	}
	if wa.Status != cdn.StatusActive || wa.Type != "webacc" || wa.CDNDomain == "" {
		t.Fatalf("start: status %s, type %q", wa.StatusName, wa.Type)
	}
	t.Logf("start: status %s, actions %d, origins %d", wa.StatusName, len(wa.DefaultRuleActions), len(wa.Upstreams))
	names := map[string]bool{}
	for _, a := range wa.DefaultRuleActions {
		names[a.Name] = true
	}
	for _, want := range []string{"minimumTls", "nosniff", "alwaysHttps"} {
		if !names[want] {
			t.Errorf("default rule action %s missing", want)
		}
	}

	t.Run("reads", func(t *testing.T) {
		found := false
		for _, item := range mustListCDN(ctx, t, client) {
			if item.CDNID == id {
				found = item.CDNDomain == wa.CDNDomain && item.Status == wa.Status
			}
		}
		if !found {
			t.Fatal("the list does not hold the CDN with the detail's domain and status")
		}
		liveCDNAnalytics(ctx, t, client, wa.CDNDomain)
	})

	t.Run("disable", func(t *testing.T) {
		start := time.Now()
		out, err := client.DisableWebAccelerator(ctx, &cdn.DisableWebAcceleratorInput{CDNID: id, NoWait: true})
		if err != nil {
			t.Fatalf("DisableWebAccelerator NoWait: %v", err)
		}
		t.Logf("disable NoWait: status %s, changed %v, %s", out.WebAccelerator.StatusName, out.Changed, time.Since(start).Round(time.Second))
		if !out.Changed || out.WebAccelerator.Status != cdn.StatusDisabling {
			t.Fatalf("disable NoWait: status %d, changed %v", out.WebAccelerator.Status, out.Changed)
		}
		_, err = client.EnableWebAccelerator(ctx, &cdn.EnableWebAcceleratorInput{CDNID: id})
		t.Logf("enable while disabling: busy %v", errors.Is(err, cdn.ErrBusy))
		if !errors.Is(err, cdn.ErrBusy) {
			t.Fatalf("enable while disabling: err = %v, want ErrBusy", err)
		}
		_, err = client.DeleteWebAccelerator(ctx, &cdn.DeleteWebAcceleratorInput{CDNID: id})
		if !errors.Is(err, cdn.ErrBusy) {
			t.Fatalf("delete while disabling: err = %v, want ErrBusy", err)
		}
		got := liveCDNSettle(ctx, t, client, id, cdn.StatusDisabled)
		t.Logf("disable settled: status %s after %s", got.StatusName, time.Since(start).Round(time.Second))
		again, err := client.DisableWebAccelerator(ctx, &cdn.DisableWebAcceleratorInput{CDNID: id})
		if err != nil || again.Changed || again.WebAccelerator.Status != cdn.StatusDisabled {
			t.Fatalf("disable again: out = %+v, err = %v", again, err)
		}
	})

	t.Run("enable", func(t *testing.T) {
		start := time.Now()
		out, err := client.EnableWebAccelerator(ctx, &cdn.EnableWebAcceleratorInput{CDNID: id})
		if err != nil {
			t.Fatalf("EnableWebAccelerator: %v", err)
		}
		t.Logf("enable: status %s, changed %v, %s", out.WebAccelerator.StatusName, out.Changed, time.Since(start).Round(time.Second))
		if !out.Changed || out.WebAccelerator.Status != cdn.StatusActive {
			t.Fatalf("enable: status %d, changed %v", out.WebAccelerator.Status, out.Changed)
		}
		again, err := client.EnableWebAccelerator(ctx, &cdn.EnableWebAcceleratorInput{CDNID: id})
		if err != nil || again.Changed {
			t.Fatalf("enable again: out = %+v, err = %v", again, err)
		}
	})

	t.Run("update", func(t *testing.T) {
		before, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: id})
		if err != nil {
			t.Fatalf("GetWebAccelerator: %v", err)
		}
		// The test account's package refuses developmentMode, so the update
		// flips browserCache between two values the package allows.
		const action = "browserCache"
		oldValue, newValue := "", "1d"
		for _, a := range before.WebAccelerator.DefaultRuleActions {
			if a.Name == action {
				oldValue = a.Value
			}
		}
		if oldValue == newValue {
			newValue = "1M"
		}
		start := time.Now()
		out, err := client.UpdateWebAccelerator(ctx, &cdn.UpdateWebAcceleratorInput{
			CDNID:          id,
			SetRuleActions: []cdn.RuleActionInput{{Name: action, Value: newValue}},
		})
		if err != nil {
			t.Fatalf("UpdateWebAccelerator: %v", err)
		}
		t.Logf("update: status %s, %s", out.WebAccelerator.StatusName, time.Since(start).Round(time.Second))
		if out.WebAccelerator.Status != cdn.StatusActive {
			t.Fatalf("update: status %d", out.WebAccelerator.Status)
		}
		oldByName := map[string]cdn.RuleAction{}
		for _, a := range before.WebAccelerator.DefaultRuleActions {
			oldByName[a.Name] = a
		}
		if len(out.WebAccelerator.DefaultRuleActions) != len(oldByName) {
			t.Errorf("actions: %d after, %d before", len(out.WebAccelerator.DefaultRuleActions), len(oldByName))
		}
		keptIDs, newIDs := 0, 0
		for _, a := range out.WebAccelerator.DefaultRuleActions {
			was, ok := oldByName[a.Name]
			if !ok {
				t.Errorf("an action appeared that was not there before")
				continue
			}
			switch {
			case a.Name == action:
				if a.Value != newValue {
					t.Errorf("%s = %q, want %q", action, a.Value, newValue)
				}
			case a.Value != was.Value:
				t.Errorf("an unrelated action changed value")
			}
			if a.ID == was.ID {
				keptIDs++
			} else {
				newIDs++
				if a.Name != "alwaysHttps" {
					t.Errorf("action %s got a new id", a.Name)
				}
			}
		}
		t.Logf("update: ids kept %d, ids changed %d", keptIDs, newIDs)
		if len(out.WebAccelerator.Upstreams) != len(before.WebAccelerator.Upstreams) ||
			out.WebAccelerator.CertificateID != before.WebAccelerator.CertificateID {
			t.Errorf("update changed the origins or the certificate")
		}
	})

	t.Run("purge", func(t *testing.T) {
		out, err := client.PurgePaths(ctx, &cdn.PurgePathsInput{
			CDNDomain: wa.CDNDomain,
			// The server refuses a bare "/" as an invalid content URI.
			Paths: []string{"/index.html"},
		})
		if err != nil {
			t.Fatalf("PurgePaths: %v", err)
		}
		if out == nil {
			t.Fatal("PurgePaths returned a nil Output")
		}
		saveLiveCDNPurge(t, purgeCapture, out)
		t.Log("purge: paths 1")
	})

	t.Run("delete", func(t *testing.T) {
		start := time.Now()
		if _, err := client.DeleteWebAccelerator(ctx, &cdn.DeleteWebAcceleratorInput{CDNID: id}); err != nil {
			t.Fatalf("DeleteWebAccelerator (active CDN): %v", err)
		}
		deleted = true
		// Deleting an ACTIVE CDN is not instant: the CDN is DELETING for
		// about five minutes, and every write is refused meanwhile.
		got, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: id})
		switch {
		case err == nil:
			t.Logf("right after delete: status %s", got.WebAccelerator.StatusName)
			if got.WebAccelerator.Status != cdn.StatusDeleting {
				t.Fatalf("right after delete: status %d, want DELETING or not found", got.WebAccelerator.Status)
			}
			_, err = client.DeleteWebAccelerator(ctx, &cdn.DeleteWebAcceleratorInput{CDNID: id})
			t.Logf("delete while deleting: busy %v", errors.Is(err, cdn.ErrBusy))
			if !errors.Is(err, cdn.ErrBusy) {
				t.Fatalf("delete while deleting: err = %v, want ErrBusy", err)
			}
		case errors.Is(err, vngcloud.ErrNotFound):
			t.Log("right after delete: not found")
		default:
			t.Fatalf("get after delete: %v", err)
		}
		deadline := time.Now().Add(10 * time.Minute)
		for {
			_, err = client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: id})
			if errors.Is(err, vngcloud.ErrNotFound) {
				break
			}
			if err != nil {
				t.Fatalf("get while deleting: %v", err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("the CDN is still there after 10 minutes")
			}
			time.Sleep(10 * time.Second)
		}
		t.Logf("not found after %s", time.Since(start).Round(time.Second))
		_, err = client.DeleteWebAccelerator(ctx, &cdn.DeleteWebAcceleratorInput{CDNID: id})
		if !errors.Is(err, vngcloud.ErrNotFound) {
			t.Fatalf("second delete: err = %v, want ErrNotFound", err)
		}
		t.Logf("web accelerators after the run: %d", len(mustListCDN(ctx, t, client)))
	})
}

func saveLiveCDNPurge(t *testing.T, captured *vngcloud.ResponseCapture, out *cdn.PurgePathsOutput) {
	t.Helper()
	if captured == nil || !json.Valid(captured.Body) {
		t.Fatal("PurgePaths did not capture a JSON response")
	}
	rawDir := repoPath("examples", "basic", "output", "raw", "cdn")
	sdkDir := repoPath("examples", "basic", "output", "sdk", "cdn")
	for _, dir := range []string{rawDir, sdkDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create purge output directory: %v", err)
		}
	}
	raw := struct {
		Operation  string          `json:"operation"`
		Method     string          `json:"method"`
		URL        string          `json:"url"`
		StatusCode int             `json:"statusCode"`
		Body       json.RawMessage `json:"body"`
	}{
		Operation: captured.Operation, Method: captured.Method, URL: captured.URL,
		StatusCode: captured.StatusCode, Body: append(json.RawMessage(nil), captured.Body...),
	}
	writeLiveCDNJSON(t, filepath.Join(rawDir, "purge_paths.json"), raw)
	writeLiveCDNJSON(t, filepath.Join(sdkDir, "purge_paths.json"), out)
}

func writeLiveCDNJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode purge output: %v", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write purge output: %v", err)
	}
}

func mustListCDN(ctx context.Context, t *testing.T, client *cdn.Client) []cdn.WebAcceleratorSummary {
	t.Helper()
	out, err := client.ListWebAccelerators(ctx, nil)
	if err != nil {
		t.Fatalf("ListWebAccelerators: %v", err)
	}
	return out.Items
}

// liveCDNStatus returns the CDN's status, or -1 when it cannot be read.
func liveCDNStatus(ctx context.Context, client *cdn.Client, id string) int {
	out, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: id})
	if err != nil {
		return -1
	}
	return out.WebAccelerator.Status
}

// liveCDNSettle reads the CDN every 10 seconds, for up to 8 minutes, until
// its status is one of the two settled values, and fails if it never is.
// want is the status the caller expects; ACTIVE also accepts DISABLED, so a
// caller can then enable it.
func liveCDNSettle(ctx context.Context, t *testing.T, client *cdn.Client, id string, want int) cdn.WebAccelerator {
	t.Helper()
	deadline := time.Now().Add(8 * time.Minute)
	for {
		out, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{CDNID: id})
		if err != nil {
			t.Fatalf("GetWebAccelerator: %v", err)
		}
		st := out.WebAccelerator.Status
		if st == want || (want == cdn.StatusActive && st == cdn.StatusDisabled) {
			return out.WebAccelerator
		}
		if time.Now().After(deadline) {
			t.Fatalf("the CDN did not reach %s: still %s", cdn.StatusName(want), out.WebAccelerator.StatusName)
		}
		time.Sleep(10 * time.Second)
	}
}
