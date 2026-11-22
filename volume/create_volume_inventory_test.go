package volume

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestCreateVolumeRejectsIncompleteIdentityInventory(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"missing totals", `{"listData":[],"page":1,"pageSize":1}`, ""},
		{"missing id", `{"listData":[{"name":"other"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":1}`, ""},
		{"missing name", `{"listData":[{"uuid":"one"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":1}`, ""},
		{"duplicate id", `{"listData":[{"uuid":"same","name":"other"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`, `{"listData":[{"uuid":"same","name":"other"}],"page":2,"pageSize":1,"totalPage":2,"totalItem":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					_, _ = w.Write([]byte(quoteVolumeFixture))
					return
				}
				body := tc.first
				if r.URL.Query().Get("page") == "2" {
					body = tc.second
				}
				_, _ = fmt.Fprint(w, body)
			}))
			in := validCreateVolumeInput()
			in.MaxPrice = 1e12
			in.NoWait = true
			_, err := c.CreateVolume(context.Background(), in)
			if err == nil || posts != 0 {
				t.Fatalf("error = %v, posts = %d", err, posts)
			}
		})
	}
}
