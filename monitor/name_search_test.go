package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/core"
)

func TestLogProjectNameSearchFindsSecondPage(t *testing.T) {
	var pages atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/log-api/v1/projects" {
			t.Errorf("request after incomplete duplicate check: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		pages.Add(1)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		name := "other"
		if page == 1 {
			name = "app"
		}
		_, _ = fmt.Fprintf(w, `{"content":[{"id":"p-%d","name":%q}],"currentPage":%d,"pageSize":1,"totalElements":2,"totalPages":2}`, page, name, page)
	}))
	_, err := client.CreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app", NoWait: true})
	if !errors.Is(err, core.ErrInvalidInput) || pages.Load() != 2 {
		t.Fatalf("error = %v, pages = %d; want duplicate refusal after two pages", err, pages.Load())
	}
}

func TestMonitorNameSearchRejectsIncompleteEvidence(t *testing.T) {
	for _, kind := range []string{"project", "alarm"} {
		for _, shape := range []string{"missing", "null", "missing-metadata", "duplicate-key", "conflicting-alias", "empty-middle", "repeated-page", "repeated-id", "changed-total", "count-mismatch", "budget"} {
			t.Run(kind+"/"+shape, func(t *testing.T) {
				var reads, writes atomic.Int64
				client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes.Add(1)
						w.WriteHeader(500)
						return
					}
					reads.Add(1)
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					_, _ = w.Write([]byte(incompleteNamePage(kind, shape, page)))
				}))
				var err error
				if kind == "project" {
					err = client.refuseIfLogProjectNameExists(context.Background(), "monitor.CreateLogProject", "app")
				} else {
					err = client.refuseIfLogAlarmNameExists(context.Background(), "monitor.CreateLogAlarm", "app")
				}
				if err == nil || writes.Load() != 0 {
					t.Fatalf("error = %v, writes = %d; want incomplete-search refusal", err, writes.Load())
				}
				if reads.Load() > 1000 {
					t.Fatalf("search reads = %d, exceeded bound", reads.Load())
				}
			})
		}
	}
}

func incompleteNamePage(kind, shape string, page int) string {
	list, pageKey, sizeKey, totalKey, pagesKey := "lstData", "page", "pageSize", "totalItem", "totalPage"
	first := 1
	if kind == "project" {
		list, pageKey, sizeKey, totalKey, pagesKey = "content", "currentPage", "pageSize", "totalElements", "totalPages"
		first = 0
	}
	items := fmt.Sprintf(`[{"id":"id-%d","name":"other"}]`, page)
	total, pages := 2, 2
	switch shape {
	case "missing":
		return `{}`
	case "null":
		items, total, pages = "null", 0, 0
	case "missing-metadata":
		return fmt.Sprintf(`{%q:[]}`, list)
	case "duplicate-key":
		return fmt.Sprintf(`{%q:[{"id":"hidden","name":"app"}],%q:[],%q:%d,%q:100,%q:0,%q:0}`, list, list, pageKey, first, sizeKey, totalKey, pagesKey)
	case "conflicting-alias":
		return fmt.Sprintf(`{%q:[{"id":"hidden","name":"app"}],%q:[],%q:%d,%q:100,%q:0,%q:0}`, list, strings.ToUpper(list), pageKey, first, sizeKey, totalKey, pagesKey)
	case "empty-middle":
		items = "[]"
	case "repeated-page":
		page = first
	case "repeated-id":
		items = `[{"id":"same","name":"other"}]`
	case "changed-total":
		if page > first {
			total = 3
		}
	case "count-mismatch":
		total, pages = 3, 1
	case "budget":
		total, pages = 1001, 1001
	}
	return fmt.Sprintf(`{%q:%s,%q:%d,%q:1,%q:%d,%q:%d}`, list, items, pageKey, page, sizeKey, totalKey, total, pagesKey, pages)
}
