package monitor

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListChannelTypesDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/notification-gateway/api/v1/type/list" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListChannelTypes.json")
	}))

	out, err := client.ListChannelTypes(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListChannelTypes() error = %v", err)
	}
	if len(out.Items) != 5 {
		t.Fatalf("unexpected types: %+v", out.Items)
	}
	want := []string{ChannelTypeEmail, ChannelTypeSlack, ChannelTypeSMS, ChannelTypeTelegram, ChannelTypeWebhook}
	for i, name := range want {
		if out.Items[i].Name != name {
			t.Fatalf("Items[%d].Name = %q, want %q", i, out.Items[i].Name, name)
		}
		if out.Items[i].ID == "" || out.Items[i].Description == "" {
			t.Fatalf("Items[%d] missing id or description: %+v", i, out.Items[i])
		}
	}
}

func TestListChannelsDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/notification-gateway/api/v1/notification/list/typeSearch" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("searchtext") != "" || q.Get("field") != "" || q.Get("type") != "" {
			t.Fatalf("unexpected filter query: %v", q)
		}
		if q.Get("page") != "1" || q.Get("size") != strconv.Itoa(core.DefaultPageSize) {
			t.Fatalf("unexpected paging query: %v", q)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListChannels.json")
	}))

	out, err := client.ListChannels(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	if out.TotalItem != 2 || out.TotalPage != 1 || out.Page != 1 || out.PageSize != 10000 {
		t.Fatalf("unexpected paging: %+v", out)
	}
	if len(out.Items) != 2 {
		t.Fatalf("unexpected items: %+v", out.Items)
	}

	webhook := out.Items[0]
	if webhook.ID != "channel-1" || webhook.Name != "example-webhook" {
		t.Fatalf("unexpected webhook channel: %+v", webhook)
	}
	if webhook.Type != ChannelTypeWebhook {
		t.Fatalf("Type = %q, want %q", webhook.Type, ChannelTypeWebhook)
	}
	if webhook.Address != "https://example.com/hooks/incoming" {
		t.Fatalf("Address = %q", webhook.Address)
	}
	if len(webhook.Headers) != 1 || webhook.Headers[0].Key != "X-Example" {
		t.Fatalf("Headers = %+v", webhook.Headers)
	}
	if webhook.UpdatedDate != "" {
		t.Fatalf("UpdatedDate = %q, want empty", webhook.UpdatedDate)
	}

	email := out.Items[1]
	if email.Type != ChannelTypeEmail {
		t.Fatalf("Type = %q, want %q", email.Type, ChannelTypeEmail)
	}
	if email.Headers != nil {
		t.Fatalf("Headers = %+v, want nil", email.Headers)
	}
	if email.UpdatedDate != "2026-09-26T16:00:00" {
		t.Fatalf("UpdatedDate = %q", email.UpdatedDate)
	}
}

// TestListChannelsSendsTypeAndPaging checks Type, Page, and Size reach the
// query string the design names, and that a nil Input is valid, as for
// ListChecksInput and ListLocationsInput.
func TestListChannelsSendsTypeAndPaging(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("type") != ChannelTypeWebhook {
			t.Fatalf("type = %q, want %q", q.Get("type"), ChannelTypeWebhook)
		}
		if q.Get("page") != "2" || q.Get("size") != "5" {
			t.Fatalf("unexpected paging query: %v", q)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListChannels.json")
	}))

	if _, err := client.ListChannels(context.Background(), &ListChannelsInput{Type: ChannelTypeWebhook, Page: 2, Size: 5}); err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
}

// TestGetChannelFoundAcrossPages checks GetChannel walks every page of
// ListChannels and matches by ID, per the design: there is no get-by-ID
// call.
func TestGetChannelFoundAcrossPages(t *testing.T) {
	calls := 0
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")
		switch page {
		case "1":
			_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-other","name":"other","address":"https://example.com/a",
				"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
				"createdDate":"2026-09-26T00:00:00"}],
				"page":1,"pageSize":10000,"totalPage":2,"totalItem":2}`))
		case "2":
			_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-target","name":"target","address":"https://example.com/b",
				"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
				"createdDate":"2026-09-26T00:00:00"}],
				"page":2,"pageSize":10000,"totalPage":2,"totalItem":2}`))
		default:
			t.Fatalf("unexpected page %q", page)
		}
	}))

	out, err := client.GetChannel(context.Background(), &GetChannelInput{ChannelID: "ch-target"})
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if out.Channel.ID != "ch-target" || out.Channel.Name != "target" {
		t.Fatalf("unexpected channel: %+v", out.Channel)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// TestGetChannelNotFound checks an account with no matching channel,
// including one with none at all, returns the SDK's not-found sentinel.
func TestGetChannelNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lstData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	_, err := client.GetChannel(context.Background(), &GetChannelInput{ChannelID: "ch-missing"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetChannel() error = %v, want ErrNotFound", err)
	}
}

func TestGetChannelMissingChannelID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing ChannelID")
	}))
	if _, err := client.GetChannel(context.Background(), &GetChannelInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetChannel() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.GetChannel(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetChannel(nil) error = %v, want ErrInvalidInput", err)
	}
}

// TestGetChannelPropagatesListError checks a list failure stops the page
// walk and reaches the caller, rather than being swallowed into NotFound.
func TestGetChannelPropagatesListError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))

	_, err := client.GetChannel(context.Background(), &GetChannelInput{ChannelID: "ch-1"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, core.ErrNotFound) {
		t.Fatal("error wraps ErrNotFound, want the server's error")
	}
}
