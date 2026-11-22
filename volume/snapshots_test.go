package volume

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestListAllSnapshotsCompletesBothPaginationLevels(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if strings.HasSuffix(r.URL.Path, "/volumes") {
			_, _ = fmt.Fprintf(w, `{"listData":[{"uuid":"volume-%d"}],"page":%d,"pageSize":1,"totalPage":2,"totalItem":2}`, page, page)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		id := parts[len(parts)-2]
		_, _ = fmt.Fprintf(w, `{"items":[{"id":"%s-snapshot-%d","volumeId":%q}],"page":%d,"pageSize":1,"totalPages":2,"totalItems":2}`, id, page, id, page)
	}))
	out, err := c.ListAllSnapshots(context.Background(), nil)
	if err != nil || out == nil || len(out.Items) != 4 || calls.Load() != 6 {
		t.Fatalf("out = %v, error = %v, calls = %d", out, err, calls.Load())
	}
}

func TestListAllSnapshotsRejectsIncompleteEvidence(t *testing.T) {
	for _, level := range []string{"volumes", "snapshots"} {
		for _, fault := range []string{"missing list", "null list", "duplicate list", "case list", "missing metadata", "repeated page", "repeated id", "changed total", "count mismatch", "empty next page", "page budget", "wrong parent", "read failure"} {
			t.Run(level+"/"+fault, func(t *testing.T) {
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					volumes := strings.HasSuffix(r.URL.Path, "/volumes")
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					key, pagesKey, totalKey, idKey := "items", "totalPages", "totalItems", "id"
					if volumes {
						key, pagesKey, totalKey, idKey = "listData", "totalPage", "totalItem", "uuid"
					}
					rows := fmt.Sprintf(`[{%q:%q,"volumeId":"volume-1"}]`, idKey, fmt.Sprintf("item-%d", page))
					if volumes {
						rows = fmt.Sprintf(`[{"uuid":"volume-%d"}]`, page)
					}
					pages, total := 1, 1
					active := volumes == (level == "volumes")
					if active {
						pages, total = 2, 2
					}
					metadata := true
					if active {
						switch fault {
						case "missing list":
							key = "unknown"
						case "null list":
							rows = "null"
						case "duplicate list":
							rows += fmt.Sprintf(`,%q:[]`, key)
						case "case list":
							rows += fmt.Sprintf(`,%q:[]`, strings.ToUpper(key))
						case "missing metadata":
							metadata = false
						case "repeated page":
							if page == 2 {
								page = 1
							}
						case "repeated id":
							rows = fmt.Sprintf(`[{%q:"volume-1","volumeId":"volume-1"}]`, idKey)
						case "changed total":
							if page == 2 {
								total = 3
							}
						case "count mismatch":
							total = 3
						case "empty next page":
							if page == 2 {
								rows = "[]"
							}
						case "page budget":
							pages, total = 100, 100
						case "wrong parent":
							if volumes {
								rows = `[{"uuid":".."}]`
							} else {
								rows = `[{"id":"snapshot-1","volumeId":"other"}]`
							}
						case "read failure":
							if page == 2 {
								w.WriteHeader(500)
								return
							}
						}
					}
					if !metadata {
						_, _ = fmt.Fprintf(w, `{%q:%s}`, key, rows)
						return
					}
					_, _ = fmt.Fprintf(w, `{%q:%s,"page":%d,"pageSize":1,%q:%d,%q:%d}`, key, rows, page, pagesKey, pages, totalKey, total)
				}))
				out, err := c.ListAllSnapshots(context.Background(), nil)
				if err == nil || out != nil {
					t.Fatalf("out = %v, error = %v", out, err)
				}
			})
		}
	}
}
