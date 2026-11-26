package globalloadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func newRotatingClient(t *testing.T, handler http.Handler, tokenFor func(int64) string, extra ...vngcloud.LoadOption) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	var tokens atomic.Int64
	api := glbRoundTrip(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		clone.URL.Path = strings.TrimPrefix(clone.URL.Path, "/glb-controller")
		clone.Host = target.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	auth := glbRoundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		switch {
		case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case r.URL.Host == "signin.greennode.ai":
			w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
			w.WriteHeader(http.StatusFound)
		default:
			n := tokens.Add(1)
			token := fmt.Sprintf("tok-%d", n)
			if tokenFor != nil {
				token = tokenFor(n)
			}
			_, _ = fmt.Fprintf(w, `{"accessToken":%q,"expiresIn":31}`, token)
		}
		return w.Result(), nil
	})
	cfg, err := vngcloud.NewConfig(append([]vngcloud.LoadOption{vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: auth}}), vngcloud.WithTransport(api), vngcloud.WithRetry(0, 0)}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	c := New(cfg)
	c.poll = time.Millisecond
	return c
}

func slowProjects(w http.ResponseWriter, _ *http.Request) {
	time.Sleep(1100 * time.Millisecond)
	_, _ = w.Write([]byte(`{"projects":[{"projectId":"proj-1","region":"hcm-3"}]}`))
}

func TestDiscoveryBearerIsScreenedAfterRotation(t *testing.T) {
	f := newFixture(t)
	f.on("GET /vserver/iam-vserver-gateway/v1/projects", slowProjects)
	f.on("GET "+glbPrefix, jsonBody(`{"id":"glb-1","package":"pkg-1","type":"Layer 4","status":"ACTIVE","note":"tok-1"}`))
	_, err := newRotatingClient(t, f, nil).CreatePool(bg(), poolInput())
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || f.writes() != 0 || strings.Contains(err.Error(), "tok-") {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
	f = newFixture(t)
	f.on("GET /vserver/iam-vserver-gateway/v1/projects", slowProjects)
	if _, err := newRotatingClient(t, f, nil).CreatePool(bg(), poolInput()); err != nil {
		t.Fatalf("clean rotation err=%v", err)
	}
}

func TestTaintedScreensTextAndOutput(t *testing.T) {
	r := newFixture(t).client(t).newRun("op")
	r.creds = []string{"secret-token"}
	if !r.tainted("a/secret-token/b") || r.tainted("clean") {
		t.Fatal("tainted")
	}
	if !r.leaksOutput(&CreatePoolOutput{Pool: Pool{ID: "x-secret-token"}}) || r.leaksOutput(&CreatePoolOutput{Pool: Pool{ID: "x"}}) {
		t.Fatal("leaksOutput")
	}
}

func TestCreateListenerConfirmationComparesPreflightBackend(t *testing.T) {
	group := "GET " + glbPrefix + "/global-pools/" + fxPool + "/pool-members/" + fxGroup
	for name, edit := range map[string]func(string) string{
		"vpc":    func(s string) string { return strings.Replace(s, `"vpcId":"vpc-1"`, `"vpcId":"vpc-9"`, 1) },
		"subnet": func(s string) string { return strings.Replace(s, `"subnetId":"sub-1"`, `"subnetId":"sub-9"`, 1) },
		"row id": func(s string) string { return strings.Replace(s, `"id":"grow-1"`, `"id":"grow-9"`, 1) },
		"region": func(s string) string { return strings.Replace(s, `"region":"hcm"`, `"region":"han"`, 1) },
		"port": func(s string) string {
			return strings.Replace(strings.Replace(s, `"port":8080`, `"port":9090`, 1), `"monitorPort":8080`, `"monitorPort":9090`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := readyFixture(t)
			f.on(group, func(w httpResponseWriter, _ *httpRequest) {
				f.mu.Lock()
				defer f.mu.Unlock()
				body := f.groupJSON()
				if f.listener {
					body = edit(body)
				}
				_, _ = w.Write([]byte(body))
			})
			out, err := f.client(t).CreateListener(bg(), listenerInput())
			if !errors.Is(err, ErrNotSettled) || out == nil || out.Listener.ID != fxListener {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestDeleteAbsenceNeedsStrictEvidence(t *testing.T) {
	for _, tc := range deleteCases() {
		exact, collection, _ := deleteKeys(tc.name)
		after := func(f *fixture, h httpHandlerFunc) httpHandlerFunc {
			return func(w httpResponseWriter, r *httpRequest) {
				if f.count(tc.key) == 0 {
					f.defaults(w, r.Method+" "+r.URL.Path)
					return
				}
				h(w, r)
			}
		}
		for name, set := range map[string]struct{ exact, list httpHandlerFunc }{
			"null row":       {status(404), jsonBody(`[null]`)},
			"row without id": {status(404), jsonBody(`[{}]`)},
			"row bad id":     {status(404), jsonBody(`[{"id":"a/b"}]`)},
			"scalar row":     {status(404), jsonBody(`["x"]`)},
			"duplicate codes": {func(w httpResponseWriter, _ *httpRequest) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"code":"internal_error","code":"x"}`))
			}, nil},
			"case alias": {func(w httpResponseWriter, _ *httpRequest) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"code":"internal_error","Code":"x"}`))
			}, nil},
			"other code": {func(w httpResponseWriter, _ *httpRequest) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"code":"boom"}`))
			}, nil},
			"not an object": {func(w httpResponseWriter, _ *httpRequest) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`["internal_error"]`))
			}, nil},
		} {
			t.Run(tc.name+": "+name, func(t *testing.T) {
				f := newFixture(t)
				tc.setup(f)
				f.on(exact, after(f, set.exact))
				if set.list != nil {
					f.on(collection, after(f, set.list))
				}
				deleted, id, err := tc.call(bg(), f.client(t))
				if deleted || !errors.Is(err, ErrNotSettled) || id == "" {
					t.Fatalf("deleted=%v id=%q err=%v", deleted, id, err)
				}
			})
		}
	}
}

func TestDeleteGroupOutcomeIsVerified(t *testing.T) {
	groups := "GET " + glbPrefix + "/global-pools/" + fxPool + "/pool-members"
	f := newFixture(t).withChildren()
	f.on(groups, func(w httpResponseWriter, r *httpRequest) {
		if f.count("DELETE ") == 0 {
			f.defaults(w, r.Method+" "+r.URL.Path)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	out, err := f.client(t).DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted {
		t.Fatalf("listener delete lost the group: out=%+v err=%v", out, err)
	}
	f = newFixture(t)
	f.pool, f.group = true, true
	f.on(groups, func(w httpResponseWriter, r *httpRequest) {
		if f.count("DELETE ") == 0 {
			f.mu.Lock()
			defer f.mu.Unlock()
			_, _ = w.Write([]byte(`[` + f.groupJSON() + `]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":"gmem-1"}]`))
	})
	pout, err := f.client(t).DeletePool(bg(), &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool})
	if !errors.Is(err, ErrNotSettled) || pout == nil || pout.Deleted {
		t.Fatalf("pool delete left the group: out=%+v err=%v", pout, err)
	}
}

func TestDeletePreflightStatesAndIDs(t *testing.T) {
	pool := "GET " + glbPrefix + "/global-pools/" + fxPool
	base := newFixture(t)
	f := newFixture(t).withChildren().on(pool, jsonBody(strings.Replace(base.poolJSON(), `"status":"ACTIVE"}`, `"status":"CREATING"}`, 1)))
	if _, err := f.client(t).DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener}); err == nil || f.writes() != 0 {
		t.Fatalf("listener delete with unstable pool health: %v", err)
	}
	f = newFixture(t).withChildren().on(pool, jsonBody(strings.Replace(base.poolJSON(), `"status":"ACTIVE","health"`, `"status":"UPDATING","health"`, 1)))
	if _, err := f.client(t).DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener}); err == nil || f.writes() != 0 {
		t.Fatalf("listener delete with unstable pool: %v", err)
	}
	f = newFixture(t)
	f.pool, f.group = true, true
	f.on("GET "+glbPrefix+"/global-pools/"+fxPool+"/pool-members", jsonBody(`[{"id":"a/b"}]`))
	if _, err := f.client(t).DeletePool(bg(), &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool}); err == nil || f.writes() != 0 || len(f.calls) > 0 && strings.Contains(strings.Join(f.calls, " "), "a/b") {
		t.Fatalf("pool delete with bad group ID: %v calls=%v", err, f.calls)
	}
}

func TestExplicitGuardValues(t *testing.T) {
	group := "GET " + glbPrefix + "/global-pools/" + fxPool + "/pool-members/" + fxGroup
	base := readyFixture(t)
	for name, body := range map[string]string{
		"backupRole missing": strings.Replace(base.groupJSON(), `,"backupRole":false`, ``, 1),
		"backupRole null":    strings.Replace(base.groupJSON(), `"backupRole":false`, `"backupRole":null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			f := readyFixture(t).on(group, jsonBody(body))
			if _, err := f.client(t).CreateListener(bg(), listenerInput()); err == nil || f.writes() != 0 {
				t.Fatalf("err=%v", err)
			}
		})
	}
	f := newFixture(t).on("GET /v1/packages", jsonBody(`[{"id":"pkg-1","enabled":true,"vlbPackages":[{"region":"hcm"}]},{"id":"pkg-1","enabled":true,"vlbPackages":[{"region":"hcm"}]}]`))
	if _, err := f.client(t).CreatePool(bg(), poolInput()); err == nil || f.writes() != 0 {
		t.Fatalf("duplicate package rows err=%v", err)
	}
}

func TestPartialOutputHoldsIDsOnly(t *testing.T) {
	f := newFixture(t)
	f.poolStatus = "CREATING"
	ctx, cancel := context.WithTimeout(bg(), 50*time.Millisecond)
	defer cancel()
	out, err := f.client(t).CreatePool(ctx, poolInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || out.Pool.Status != "" || out.Pool.Name != "" || out.PoolMember.Status != "" || len(out.PoolMember.Members) != 0 {
		t.Fatalf("interrupted pool create out=%+v", out)
	}
	f = newFixture(t)
	f.poolStatus = "ERROR"
	out, err = f.client(t).CreatePool(bg(), poolInput())
	if !errors.Is(err, ErrFailed) || out.Pool.ID != fxPool || out.Pool.Status != "" || out.Pool.Health != nil {
		t.Fatalf("failed pool create out=%+v err=%v", out, err)
	}
	g := readyFixture(t)
	g.listenerStatus = "CREATING"
	ctx2, cancel2 := context.WithTimeout(bg(), 50*time.Millisecond)
	defer cancel2()
	lout, err := g.client(t).CreateListener(ctx2, listenerInput())
	if !errors.Is(err, ErrNotSettled) || lout.Listener.ID != fxListener || lout.Listener.Status != "" || lout.Listener.Name != "" {
		t.Fatalf("interrupted listener create out=%+v", lout)
	}
	g = readyFixture(t)
	g.listenerStatus = "ERROR"
	lout, err = g.client(t).CreateListener(bg(), listenerInput())
	if !errors.Is(err, ErrFailed) || lout.Listener.ID != fxListener || lout.Listener.Status != "" {
		t.Fatalf("failed listener create out=%+v", lout)
	}
}

func TestRotatedBearerNeverRidesAHeldValue(t *testing.T) {
	f := newFixture(t).withChildren()
	var carried atomic.Int64
	f.pool, f.group, f.listener = true, true, false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path+r.URL.RawQuery, "gpool-1") && r.Header.Get("Authorization") == "Bearer gpool-1" {
			carried.Add(1)
		}
		if r.Method == http.MethodGet && r.URL.Path == glbPrefix+"/global-listeners" {
			time.Sleep(1100 * time.Millisecond)
		}
		f.ServeHTTP(w, r)
	})
	tokens := func(n int64) string {
		if n == 1 {
			return "tok-1"
		}
		return "gpool-1"
	}
	c := newRotatingClient(t, h, tokens, vngcloud.WithProjectID(fxProject))
	out, err := c.DeletePool(bg(), &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool})
	if err == nil || carried.Load() != 0 || f.writes() != 0 || (out != nil && out.Deleted) {
		t.Fatalf("out=%+v err=%v carried=%d calls=%v", out, err, carried.Load(), f.calls)
	}
}

func TestDeleteOutputsAreScreened(t *testing.T) {
	r := newFixture(t).client(t).newRun("op")
	r.creds = []string{"glis-1"}
	out, err := finishOutput(r, &DeleteListenerOutput{LoadBalancerID: fxGLB, ListenerID: "glis-1"}, nil)
	if out != nil || !errors.Is(err, ErrNotSettled) {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDeleteAbsenceRejectsDuplicateRowsAndWrongKeyCase(t *testing.T) {
	for _, tc := range deleteCases() {
		exact, collection, _ := deleteKeys(tc.name)
		after := func(f *fixture, h httpHandlerFunc) httpHandlerFunc {
			return func(w httpResponseWriter, r *httpRequest) {
				if f.count(tc.key) == 0 {
					f.defaults(w, r.Method+" "+r.URL.Path)
					return
				}
				h(w, r)
			}
		}
		t.Run(tc.name+": duplicate rows", func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on(exact, after(f, status(404)))
			f.on(collection, after(f, jsonBody(`[{"id":"other"},{"id":"other"}]`)))
			deleted, _, err := tc.call(bg(), f.client(t))
			if deleted || !errors.Is(err, ErrNotSettled) {
				t.Fatalf("deleted=%v err=%v", deleted, err)
			}
		})
		t.Run(tc.name+": capital Code", func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on(exact, after(f, func(w httpResponseWriter, _ *httpRequest) {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"Code":"internal_error"}`))
			}))
			deleted, _, err := tc.call(bg(), f.client(t))
			if deleted || !errors.Is(err, ErrNotSettled) {
				t.Fatalf("deleted=%v err=%v", deleted, err)
			}
		})
	}
}

func TestGroupListErrorNeverProvesPoolAbsence(t *testing.T) {
	f := newFixture(t)
	f.pool, f.group = true, true
	after := func(h httpHandlerFunc) httpHandlerFunc {
		return func(w httpResponseWriter, r *httpRequest) {
			if f.count("DELETE ") == 0 {
				f.defaults(w, r.Method+" "+r.URL.Path)
				return
			}
			h(w, r)
		}
	}
	f.on("GET "+glbPrefix+"/global-pools/"+fxPool, after(status(404)))
	f.on("GET "+glbPrefix+"/global-pools", after(jsonBody(`[{"id":"gpool-1"}]`)))
	f.on("GET "+glbPrefix+"/global-pools/"+fxPool+"/pool-members", after(status(404)))
	ctx, cancel := context.WithTimeout(bg(), 100*time.Millisecond)
	defer cancel()
	out, err := f.client(t).DeletePool(ctx, &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
