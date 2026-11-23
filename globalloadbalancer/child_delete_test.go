package globalloadbalancer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

type deleteCase struct {
	name  string
	setup func(*fixture)
	call  func(context.Context, *Client) (bool, string, error)
	key   string
	gone  func(*fixture) bool
}

func deleteCases() []deleteCase {
	return []deleteCase{
		{
			name: "listener", key: "DELETE " + glbPrefix + "/global-listeners/" + fxListener,
			setup: func(f *fixture) { f.withChildren() },
			call: func(ctx context.Context, c *Client) (bool, string, error) {
				out, err := c.DeleteListener(ctx, &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener})
				if out == nil {
					return false, "", err
				}
				return out.Deleted, out.LoadBalancerID + "/" + out.ListenerID, err
			},
			gone: func(f *fixture) bool { return !f.listener && f.pool && f.group },
		},
		{
			name: "pool", key: "DELETE " + glbPrefix + "/global-pools/" + fxPool,
			setup: func(f *fixture) { f.pool, f.group = true, true },
			call: func(ctx context.Context, c *Client) (bool, string, error) {
				out, err := c.DeletePool(ctx, &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool})
				if out == nil {
					return false, "", err
				}
				return out.Deleted, out.LoadBalancerID + "/" + out.PoolID, err
			},
			gone: func(f *fixture) bool { return !f.pool && !f.group },
		},
	}
}

func TestDeletesSendOnceAndConfirmAbsence(t *testing.T) {
	ids := map[string]string{"listener": "glb-1/glis-1", "pool": "glb-1/gpool-1"}
	for _, tc := range deleteCases() {
		for _, code := range []int{200, 202} {
			t.Run(tc.name, func(t *testing.T) {
				f := newFixture(t)
				f.acceptStatus = code
				tc.setup(f)
				deleted, id, err := tc.call(bg(), f.client(t))
				if err != nil || !deleted || id != ids[tc.name] || f.count("DELETE ") != 1 || f.count(tc.key) != 1 || f.count("POST ") != 0 || !tc.gone(f) {
					t.Fatalf("deleted=%v id=%q err=%v calls=%v", deleted, id, err, f.calls)
				}
				if len(f.body(tc.key)) != 0 {
					t.Fatalf("DELETE carried a body")
				}
				first := 0
				for i, c := range f.calls {
					if c == tc.key {
						first = i
					}
				}
				if first == 0 {
					t.Fatalf("no preflight: %v", f.calls)
				}
			})
		}
	}
}

func TestDeleteRefusesBeforeAnyWrite(t *testing.T) {
	base := newFixture(nil).withChildren()
	listenerGet := "GET " + glbPrefix + "/global-listeners/" + fxListener
	poolGet := "GET " + glbPrefix + "/global-pools/" + fxPool
	groupGet := poolGet + "/pool-members/" + fxGroup
	groups := poolGet + "/pool-members"
	pools := "GET " + glbPrefix + "/global-pools"
	listeners := "GET " + glbPrefix + "/global-listeners"
	glb := "GET " + glbPrefix
	layer7 := `{"id":"glb-1","package":"pkg-1","type":"Layer 7","status":"ACTIVE"}`
	for name, set := range map[string]struct {
		op    string
		key   string
		body  string
		extra func(*fixture)
	}{
		"listener: glb layer 7":       {"listener", glb, layer7, nil},
		"listener: other id":          {"listener", listenerGet, strings.Replace(base.listenerJSON(), `"id":"glis-1"`, `"id":"glis-2"`, 1), nil},
		"listener: not active":        {"listener", listenerGet, strings.Replace(base.listenerJSON(), `"status":"ACTIVE"`, `"status":"CREATING"`, 1), nil},
		"listener: udp":               {"listener", listenerGet, strings.Replace(base.listenerJSON(), `"protocol":"TCP"`, `"protocol":"UDP"`, 1), nil},
		"listener: other parent":      {"listener", listenerGet, strings.Replace(base.listenerJSON(), `"globalLoadBalancerId":"glb-1"`, `"globalLoadBalancerId":"glb-2"`, 1), nil},
		"listener: pool binding gone": {"listener", listenerGet, strings.Replace(base.listenerJSON(), `"globalPoolId":"gpool-1"`, `"globalPoolId":"gpool-9"`, 1), nil},
		"listener: two listeners":     {"listener", listeners, `[` + base.listenerJSON() + `,{"id":"glis-2"}]`, nil},
		"listener: two pools":         {"listener", pools, `[` + base.poolJSON() + `,{"id":"gpool-2"}]`, nil},
		"pool: listener present":      {"pool", listeners, `[` + base.listenerJSON() + `]`, nil},
		"pool: no group":              {"pool", groups, `[]`, nil},
		"pool: two groups":            {"pool", groups, `[` + base.groupJSON() + `,{"id":"gmem-2"}]`, nil},
		"pool: group public":          {"pool", groupGet, strings.Replace(base.groupJSON(), `PRIVATE`, `PUBLIC`, 1), nil},
		"pool: group status":          {"pool", groupGet, strings.Replace(base.groupJSON(), `"status":"OFFLINE","members"`, `"status":"ERROR","members"`, 1), nil},
		"pool: two rows":              {"pool", groupGet, strings.Replace(base.groupJSON(), `"members":[`, `"members":[{"id":"x"},`, 1), nil},
		"pool: health not active":     {"pool", poolGet, strings.Replace(base.poolJSON(), `"status":"ACTIVE"}`, `"status":"CREATING"}`, 1), nil},
		"pool: not tcp":               {"pool", poolGet, strings.Replace(base.poolJSON(), `"protocol":"TCP","status"`, `"protocol":"HTTP","status"`, 1), nil},
		"pool: other id":              {"pool", poolGet, strings.Replace(base.poolJSON(), `"id":"gpool-1","name"`, `"id":"gpool-2","name"`, 1), nil},
		"pool: two pools":             {"pool", pools, `[` + base.poolJSON() + `,{"id":"gpool-2"}]`, nil},
		"pool: null members":          {"pool", groups, `null`, nil},
		"pool: credential":            {"pool", poolGet, strings.Replace(base.poolJSON(), `"tcp-pool"`, `"x-synthetic"`, 1), nil},
	} {
		t.Run(name, func(t *testing.T) {
			var tc deleteCase
			for _, c := range deleteCases() {
				if c.name == set.op {
					tc = c
				}
			}
			f := newFixture(t)
			f.pool, f.group = true, true
			f.on(set.key, jsonBody(set.body))
			if strings.HasPrefix(name, "pool: listener") {
				f.listener = true
			}
			deleted, _, err := tc.call(bg(), f.client(t))
			if err == nil || deleted || f.writes() != 0 || errors.Is(err, ErrNotSettled) || strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("deleted=%v err=%v calls=%v", deleted, err, f.calls)
			}
		})
	}
}

func TestDeletePreflightNotFoundSendsNothing(t *testing.T) {
	for _, tc := range deleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t) // no children exist
			deleted, _, err := tc.call(bg(), f.client(t))
			if deleted || !errors.Is(err, vngcloud.ErrNotFound) || f.writes() != 0 {
				t.Fatalf("deleted=%v err=%v calls=%v", deleted, err, f.calls)
			}
		})
	}
}

func TestDeleteDefinitiveRejectionAndAmbiguity(t *testing.T) {
	for _, tc := range deleteCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, code := range []int{400, 403, 409} {
				f := newFixture(t)
				tc.setup(f)
				f.on(tc.key, func(w httpResponseWriter, _ *httpRequest) {
					w.WriteHeader(code)
					_, _ = w.Write([]byte(`{"message":"glis-1 gpool-1 secret-value"}`))
				})
				deleted, _, err := tc.call(bg(), f.client(t))
				var api *vngcloud.APIError
				if deleted || !errors.As(err, &api) || api.StatusCode != code || errors.Is(err, ErrNotSettled) || f.count(tc.key) != 1 || strings.Contains(err.Error(), "glis-1") || strings.Contains(err.Error(), "secret-value") {
					t.Fatalf("code=%d deleted=%v err=%v", code, deleted, err)
				}
			}
			for _, h := range []httpHandlerFunc{status(500), status(503), func(w httpResponseWriter, _ *httpRequest) { w.Header().Set("Location", "/x"); w.WriteHeader(307) }} {
				f := newFixture(t)
				tc.setup(f)
				f.on(tc.key, h)
				deleted, id, err := tc.call(bg(), f.client(t))
				if deleted || !errors.Is(err, ErrNotSettled) || id == "" || f.count(tc.key) != 1 {
					t.Fatalf("deleted=%v id=%q err=%v", deleted, id, err)
				}
			}
		})
	}
}

func TestDeleteAbsenceMustBeProven(t *testing.T) {
	for _, tc := range deleteCases() {
		exact := map[string]string{
			"listener": "GET " + glbPrefix + "/global-listeners/" + fxListener,
			"pool":     "GET " + glbPrefix + "/global-pools/" + fxPool,
		}[tc.name]
		collection := map[string]string{
			"listener": "GET " + glbPrefix + "/global-listeners",
			"pool":     "GET " + glbPrefix + "/global-pools",
		}[tc.name]
		afterDelete := func(f *fixture, h httpHandlerFunc) httpHandlerFunc {
			return func(w httpResponseWriter, r *httpRequest) {
				if f.count(tc.key) == 0 {
					f.defaults(w, r.Method+" "+r.URL.Path)
					return
				}
				h(w, r)
			}
		}
		for name, mutate := range map[string]func(*fixture){
			"exact id differs": func(f *fixture) { f.on(exact, afterDelete(f, jsonBody(`{"id":"other","status":"ACTIVE"}`))) },
			"exact unknown state": func(f *fixture) {
				f.on(exact, afterDelete(f, jsonBody(`{"id":"`+map[string]string{"listener": fxListener, "pool": fxPool}[tc.name]+`","status":"WEIRD"}`)))
			},
			"exact forbidden": func(f *fixture) { f.on(exact, afterDelete(f, status(403))) },
			"collection forbidden": func(f *fixture) {
				f.on(exact, afterDelete(f, status(404)))
				f.on(collection, afterDelete(f, status(403)))
			},
			"parent gone": func(f *fixture) {
				f.on(exact, afterDelete(f, status(404)))
				f.on("GET "+glbPrefix, afterDelete(f, status(404)))
			},
			"parent forbidden": func(f *fixture) {
				f.on(exact, afterDelete(f, status(404)))
				f.on("GET "+glbPrefix, afterDelete(f, status(403)))
			},
		} {
			t.Run(tc.name+": "+name, func(t *testing.T) {
				f := newFixture(t)
				tc.setup(f)
				mutate(f)
				deleted, id, err := tc.call(bg(), f.client(t))
				if deleted || !errors.Is(err, ErrNotSettled) || id == "" || f.count(tc.key) != 1 {
					t.Fatalf("deleted=%v id=%q err=%v calls=%v", deleted, id, err, f.calls)
				}
			})
		}
	}
}

func TestDeleteSurvivorMissingIsNotSettled(t *testing.T) {
	f := newFixture(t).withChildren()
	pool := "GET " + glbPrefix + "/global-pools/" + fxPool
	f.on(pool, func(w httpResponseWriter, r *httpRequest) {
		if f.count("DELETE ") == 0 {
			f.defaults(w, "GET "+r.URL.Path)
			return
		}
		w.WriteHeader(404)
	})
	out, err := f.client(t).DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted || out.ListenerID != fxListener {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestDeleteWaitsWhileTargetPersists(t *testing.T) {
	f := newFixture(t).withChildren()
	listener := "GET " + glbPrefix + "/global-listeners/" + fxListener
	reads := 0
	f.on(listener, func(w httpResponseWriter, r *httpRequest) {
		reads++
		if f.count("DELETE ") == 0 || reads < 4 {
			f.mu.Lock()
			defer f.mu.Unlock()
			_, _ = w.Write([]byte(f.listenerJSON()))
			return
		}
		w.WriteHeader(404)
	})
	out, err := f.client(t).DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener})
	if err != nil || out == nil || !out.Deleted || reads < 4 {
		t.Fatalf("out=%+v err=%v reads=%d", out, err, reads)
	}
}

func TestDeleteRejectsBadIDsBeforeRequests(t *testing.T) {
	c := newFixture(t)
	cl := c.client(t)
	_, e1 := cl.DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: "a/b"})
	_, e3 := cl.DeletePool(bg(), &DeletePoolInput{LoadBalancerID: "a b", PoolID: fxPool})
	_, e4 := cl.DeletePool(bg(), &DeletePoolInput{LoadBalancerID: fxGLB})
	_, e5 := cl.DeletePool(bg(), nil)
	for i, err := range []error{e1, e3, e4, e5} {
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("case %d err=%v", i, err)
		}
	}
	if len(c.calls) != 0 {
		t.Fatalf("calls=%v", c.calls)
	}
}

func TestDeletePoolAcceptsAnyMemberHealth(t *testing.T) {
	for _, health := range []string{"ONLINE", "OFFLINE", "HEALTHY", "UNHEALTHY"} {
		f := newFixture(t)
		f.pool, f.group, f.groupStatus = true, true, health
		deleted, _, err := deleteCases()[1].call(bg(), f.client(t))
		if err != nil || !deleted || f.count("DELETE ") != 1 {
			t.Fatalf("health=%s deleted=%v err=%v", health, deleted, err)
		}
	}
}

func TestDeleteAbsenceFollowsCollection(t *testing.T) {
	for _, tc := range deleteCases() {
		exact, collection, id := deleteKeys(tc.name)
		fail500 := func(w httpResponseWriter, _ *httpRequest) {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"code":"internal_error","message":"Internal error"}`))
		}
		after := func(f *fixture, h httpHandlerFunc) httpHandlerFunc {
			return func(w httpResponseWriter, r *httpRequest) {
				if f.count(tc.key) == 0 {
					f.defaults(w, r.Method+" "+r.URL.Path)
					return
				}
				h(w, r)
			}
		}
		t.Run(tc.name+": 500 detail and list omits the ID", func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on(exact, after(f, fail500))
			deleted, _, err := tc.call(bg(), f.client(t))
			if err != nil || !deleted {
				t.Fatalf("deleted=%v err=%v", deleted, err)
			}
		})
		t.Run(tc.name+": 500 detail and list holds the ID", func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on(exact, after(f, fail500))
			f.on(collection, after(f, jsonBody(`[{"id":"`+id+`"}]`)))
			ctx, cancel := context.WithTimeout(bg(), 100*time.Millisecond)
			defer cancel()
			deleted, got, err := tc.call(ctx, f.client(t))
			if deleted || got == "" || !errors.Is(err, ErrNotSettled) || f.count(exact) < 3 {
				t.Fatalf("deleted=%v id=%q err=%v exact reads=%d", deleted, got, err, f.count(exact))
			}
		})
		t.Run(tc.name+": 404 detail and list holds the ID", func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on(exact, after(f, status(404)))
			f.on(collection, after(f, jsonBody(`[{"id":"`+id+`"}]`)))
			ctx, cancel := context.WithTimeout(bg(), 100*time.Millisecond)
			defer cancel()
			deleted, _, err := tc.call(ctx, f.client(t))
			if deleted || !errors.Is(err, ErrNotSettled) {
				t.Fatalf("deleted=%v err=%v", deleted, err)
			}
		})
		t.Run(tc.name+": plain 500 detail is not absence", func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.on(exact, after(f, status(500)))
			deleted, _, err := tc.call(bg(), f.client(t))
			if deleted || !errors.Is(err, ErrNotSettled) {
				t.Fatalf("deleted=%v err=%v", deleted, err)
			}
		})
	}
}

func deleteKeys(name string) (exact, collection, id string) {
	if name == "listener" {
		return "GET " + glbPrefix + "/global-listeners/" + fxListener, "GET " + glbPrefix + "/global-listeners", fxListener
	}
	return "GET " + glbPrefix + "/global-pools/" + fxPool, "GET " + glbPrefix + "/global-pools", fxPool
}
