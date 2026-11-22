package database

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestDeleteWire(t *testing.T) {
	s := &script{detail: []string{detailBody("ACTIVE", nil), detailBody("ACTIVE", nil), detailBody("DELETING", nil), "404"}}
	h := newHarness(t, s.handler())
	out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
	if err != nil || out.ID != "db-1" || !out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
	var del *recorded
	order := []string{}
	for _, r := range h.log() {
		r := r
		order = append(order, r.Method+" "+r.Path)
		if r.Method == http.MethodPost {
			if del != nil {
				t.Fatal("more than one POST")
			}
			del = &r
		}
	}
	if del == nil || del.Path != pathDelete {
		t.Fatalf("log %v", order)
	}
	var got, want any
	_ = json.Unmarshal([]byte(del.Body), &got)
	_ = json.Unmarshal([]byte(`{"databaseInstances":[{"instancesId":"db-1","config":{"createFinalBackup":false,"deleteAllBackup":false}}],"action":"delete","resType":"dbaas"}`), &want)
	if !reflect.DeepEqual(got, want) || del.Header.Get("Authorization") != "Bearer bearer-synthetic" {
		t.Fatalf("body %s headers %v", del.Body, del.Header)
	}
	if order[0] != "GET "+pathDetail || h.count("GET", pathBackups) != 1 {
		t.Fatalf("preflight order %v", order)
	}
}

func TestDeleteDeletedStateConfirms(t *testing.T) {
	s := &script{detail: []string{detailBody("ACTIVE", nil), detailBody("DELETING", nil), detailBody("DELETED", nil)}}
	h := newHarness(t, s.handler())
	out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
	if err != nil || !out.Deleted {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestDeleteInvalidInputSendsNothing(t *testing.T) {
	for _, id := range []string{"", "a/b", "..", "a b"} {
		h := newHarness(t, (&script{}).handler())
		out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: id})
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) || len(h.log()) != 0 {
			t.Fatalf("id %q out=%v err=%v", id, out, err)
		}
	}
	h := newHarness(t, (&script{}).handler())
	if _, err := h.client.DeleteDatabaseInstance(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) || len(h.log()) != 0 {
		t.Fatalf("err=%v", err)
	}
}

func TestDeletePreflightRefusals(t *testing.T) {
	cases := []struct {
		name    string
		detail  string
		backups string
		refused bool
	}{
		{"building", detailBody("BUILDING", nil), "", true},
		{"deleting", detailBody("DELETING", nil), "", true},
		{"error", detailBody("ERROR", nil), "", true},
		{"other id", detailBody("ACTIVE", map[string]any{"id": "db-2"}), "", false},
		{"not mysql", detailBody("ACTIVE", map[string]any{"datastoreType": "PostgreSQL"}), "", true},
		{"replicas", detailBody("ACTIVE", map[string]any{"replicas": []string{"db-9"}}), "", true},
		{"is replica", detailBody("ACTIVE", map[string]any{"replicaSourceId": "db-9"}), "", true},
		{"shared", detailBody("ACTIVE", map[string]any{"owner": false}), "", true},
		{"already deleted", detailBody("ACTIVE", map[string]any{"deleted": true}), "", true},
		{"has backups", detailBody("ACTIVE", nil), `{"code":200,"data":[{"id":"bk-1"}]}`, true},
		{"backups malformed", detailBody("ACTIVE", nil), `{"code":200,"data":null}`, false},
		{"detail missing", "404", "", false},
		{"other project", detailBody("ACTIVE", map[string]any{"projectId": "project-2"}), "", true},
		{"missing zone", detailBody("ACTIVE", map[string]any{"zoneId": nil}), "", true},
		{"unknown zone", detailBody("ACTIVE", map[string]any{"zoneId": "zone-9"}), "", true},
		{"redirect preflight", "302", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{detail: []string{tc.detail}, backups: tc.backups}
			h := newHarness(t, s.handler())
			out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
			if out != nil && out.Deleted || err == nil || h.posts() != 0 || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
			}
			if tc.refused && !errors.Is(err, ErrDeleteRefused) {
				t.Fatalf("want ErrDeleteRefused, got %v", err)
			}
		})
	}
	t.Run("backup read fails", func(t *testing.T) {
		base := (&script{detail: []string{detailBody("ACTIVE", nil)}}).handler()
		h := newHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) {
			if r.URL.Path == pathBackups {
				writeJSON(w, 500, `{"message":"boom"}`)
				return
			}
			base(w, r, h)
		})
		_, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
		if err == nil || h.posts() != 0 {
			t.Fatalf("err=%v posts=%d", err, h.posts())
		}
	})
}

func TestDeleteSendsOnce(t *testing.T) {
	cases := []struct {
		name    string
		respond func(w http.ResponseWriter, r *http.Request)
		api     int
		settled bool
	}{
		{"401", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 401, `{}`) }, 401, false},
		{"403", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 403, `{}`) }, 403, false},
		{"429", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 429, `{}`) }, 429, false},
		{"500", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 500, `{}`) }, 0, true},
		{"302", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://"+r.Host+"/elsewhere")
			w.WriteHeader(302)
		}, 0, true},
		{"307", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "http://"+r.Host+pathDelete)
			w.WriteHeader(307)
		}, 0, true},
		{"dropped", func(w http.ResponseWriter, _ *http.Request) {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}, 0, true},
		{"success false", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, `{"code":200,"data":[{"databaseInstances":"db-1","action":"delete","success":false,"errorMsg":"busy"}]}`)
		}, 200, false},
		{"bearer echoed", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, `{"message":"bearer-synthetic"}`)
		}, 0, true},
		{"escaped bearer echoed", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, `{"message":"\u0062earer-synthetic"}`)
		}, 0, true},
		{"one of two rows rejected", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, `{"data":[{"success":true},{"success":false}]}`)
		}, 200, false},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, `<html>`) }, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{del: tc.respond, detail: []string{detailBody("ACTIVE", nil)}, detailDefault: detailBody("ACTIVE", nil)}
			h := newHarness(t, s.handler())
			out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
			if h.posts() != 1 || h.count("POST", "/elsewhere") != 0 || (out != nil && out.Deleted) {
				t.Fatalf("posts=%d out=%v err=%v", h.posts(), out, err)
			}
			if tc.settled {
				if !errors.Is(err, ErrNotSettled) || out == nil || out.ID != "db-1" || h.count("GET", pathDetail) != 1 {
					t.Fatalf("out=%v err=%v reads=%d", out, err, h.count("GET", pathDetail))
				}
				return
			}
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != tc.api || errors.Is(err, ErrNotSettled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDeleteWaits(t *testing.T) {
	active := detailBody("ACTIVE", nil)
	deleting := detailBody("DELETING", nil)
	cases := []struct {
		name    string
		detail  []string
		def     string
		cancel  int
		deleted bool
		check   func(t *testing.T, h *harness)
	}{
		{name: "gone after deleting", detail: []string{active, active, deleting, deleting, "404"}, deleted: true, check: func(t *testing.T, h *harness) {
			if len(h.clock.sleeps) != 3 {
				t.Fatalf("sleeps %v", h.clock.sleeps)
			}
			for _, d := range h.clock.sleeps {
				if d != 10*time.Second {
					t.Fatalf("sleep %v", d)
				}
			}
		}},
		{name: "timeout", detail: []string{active}, def: deleting, check: func(t *testing.T, h *harness) {
			var total time.Duration
			for _, d := range h.clock.sleeps {
				total += d
			}
			if total != 30*time.Minute {
				t.Fatalf("total %v", total)
			}
		}},
		{name: "redirect to a 404", detail: []string{active, "302"}, check: func(t *testing.T, h *harness) {
			if h.count("GET", "/elsewhere") != 0 {
				t.Fatal("followed the redirect")
			}
		}},
		{name: "unknown state", detail: []string{active, detailBody("WEIRD", nil)}},
		{name: "other id", detail: []string{active, detailBody("DELETING", map[string]any{"id": "db-2"})}},
		{name: "read error", detail: []string{active, active, "not json"}},
		{name: "cancel", detail: []string{active}, def: deleting, cancel: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{detail: tc.detail, detailDefault: tc.def}
			h := newHarness(t, s.handler())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel > 0 {
				h.clock.onSlp = func(n int) {
					if n == tc.cancel {
						cancel()
					}
				}
			}
			out, err := h.client.DeleteDatabaseInstance(ctx, &DeleteDatabaseInstanceInput{ID: "db-1"})
			if tc.deleted {
				if err != nil || !out.Deleted {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted || out.ID != "db-1" {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if h.posts() != 1 {
				t.Fatalf("posts %d", h.posts())
			}
			if tc.check != nil {
				tc.check(t, h)
			}
		})
	}
}

func TestDeleteAcceptsAnyBodyAndConfirmsByRead(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"empty object":       func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, `{}`) },
		"unrecognized shape": func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, `{"result":"ok","data":{"x":1}}`) },
		"other row": func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, `{"code":200,"data":[{"databaseInstances":"db-2","success":true}]}`)
		},
		"empty 202": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) },
		"empty 204": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) },
	}
	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			s := &script{del: respond, detail: []string{detailBody("ACTIVE", map[string]any{"owner": json.RawMessage("null")}), "404"}}
			h := newHarness(t, s.handler())
			out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
			if err != nil || !out.Deleted || h.posts() != 1 {
				t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
			}
		})
	}
}

func TestDeleteAllowsNullOrAbsentOwner(t *testing.T) {
	for name, owner := range map[string]any{"null": json.RawMessage("null"), "absent": nil} {
		t.Run(name, func(t *testing.T) {
			s := &script{detail: []string{detailBody("ACTIVE", map[string]any{"owner": owner}), "404"}}
			h := newHarness(t, s.handler())
			out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
			if err != nil || !out.Deleted || h.posts() != 1 {
				t.Fatalf("out=%v err=%v", out, err)
			}
		})
	}
}

func TestDeleteReadsNeverShowTheBearer(t *testing.T) {
	leaky := strings.Replace(detailBody("ACTIVE", nil), `"demo-db"`, `"x-bearer-synthetic-y"`, 1)
	t.Run("preflight", func(t *testing.T) {
		h := newHarness(t, (&script{detail: []string{leaky}}).handler())
		out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
		if out != nil || err == nil || errors.Is(err, ErrNotSettled) || h.posts() != 0 || strings.Contains(err.Error(), "bearer-synthetic") {
			t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
		}
	})
	t.Run("wait", func(t *testing.T) {
		h := newHarness(t, (&script{detail: []string{detailBody("ACTIVE", nil), leaky}}).handler())
		out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
		if !errors.Is(err, ErrNotSettled) || out == nil || out.Deleted || strings.Contains(err.Error(), "bearer-synthetic") {
			t.Fatalf("out=%v err=%v", out, err)
		}
	})
}

func TestDeleteDiscoversTheProjectOnlyWithDefaultEndpoints(t *testing.T) {
	s := &script{detail: []string{detailBody("ACTIVE", nil), "404"}}
	h := newHarnessOpts(t, s.handler(), false)
	out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
	if err != nil || !out.Deleted || h.count("GET", "/vserver/iam-vserver-gateway/v1/projects") != 1 {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestDeleteProvesTheProjectFromTheList(t *testing.T) {
	two := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageNumber") == "2" {
			writeJSON(w, 200, listPage("project-1", 2, 2, 2, "db-1"))
			return
		}
		writeJSON(w, 200, listPage("project-1", 1, 2, 2, "db-0"))
	}
	fail := func(status int, body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, status, body) }
	}
	cases := []struct {
		name    string
		detail  map[string]any
		list    func(http.ResponseWriter, *http.Request)
		proceed bool
		pages   int
	}{
		{"empty detail project proven by list", map[string]any{"projectId": nil}, nil, true, 1},
		{"empty string detail project proven by list", map[string]any{"projectId": ""}, nil, true, 1},
		{"two pages with the ID on page 2", map[string]any{"projectId": nil}, two, true, 2},
		{"list project differs", nil, fail(200, listPage("project-2", 1, 1, 1, "db-1")), false, 1},
		{"list project missing", nil, fail(200, listPage("", 1, 1, 1, "db-1")), false, 1},
		{"ID absent", nil, fail(200, listPage("project-1", 1, 1, 1, "db-9")), false, 1},
		{"empty list", nil, fail(200, listPage("project-1", 1, 0, 0)), false, 1},
		{"duplicate ID", nil, fail(200, listPage("project-1", 1, 1, 2, "db-1", "db-1")), false, 1},
		{"page set shorter than the total", nil, fail(200, listPage("project-1", 1, 1, 5, "db-1")), false, 1},
		{"wrong page number", nil, fail(200, listPage("project-1", 3, 1, 1, "db-1")), false, 1},
		{"list read fails", nil, fail(500, `{"message":"boom"}`), false, 1},
		{"list redirects", nil, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(302)
		}, false, 1},
		{"page 2 fails", nil, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("pageNumber") == "2" {
				writeJSON(w, 500, `{}`)
				return
			}
			writeJSON(w, 200, listPage("project-1", 1, 2, 2, "db-0"))
		}, false, 2},
		{"total pages change", nil, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("pageNumber") == "2" {
				writeJSON(w, 200, listPage("project-1", 2, 3, 2, "db-1"))
				return
			}
			writeJSON(w, 200, listPage("project-1", 1, 2, 2, "db-0"))
		}, false, 2},
		{"project changes between pages", nil, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("pageNumber") == "2" {
				writeJSON(w, 200, listPage("project-2", 2, 2, 2, "db-1"))
				return
			}
			writeJSON(w, 200, listPage("project-1", 1, 2, 2, "db-0"))
		}, false, 2},
		{"ID on both pages", nil, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, 200, listPage("project-1", map[bool]int{true: 2, false: 1}[r.URL.Query().Get("pageNumber") == "2"], 2, 2, "db-1"))
		}, false, 2},
		{"detail project differs although the list matches", map[string]any{"projectId": "project-2"}, nil, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &script{list: tc.list, detail: []string{detailBody("ACTIVE", tc.detail), "404"}}
			h := newHarness(t, s.handler())
			out, err := h.client.DeleteDatabaseInstance(context.Background(), &DeleteDatabaseInstanceInput{ID: "db-1"})
			if tc.proceed {
				if err != nil || !out.Deleted || h.posts() != 1 {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else if err == nil || h.posts() != 0 || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v posts=%d", out, err, h.posts())
			}
			if got := h.count("GET", pathList); got != tc.pages {
				t.Fatalf("list reads %d, want %d", got, tc.pages)
			}
			if h.count("GET", "/elsewhere") != 0 {
				t.Fatal("followed a redirect")
			}
		})
	}
}
