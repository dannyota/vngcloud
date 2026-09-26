package dns

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// zoneBody builds a {"data": {...}} hosted zone envelope with the given
// status, description, and VPC ids, matching the shape GetHostedZone
// decodes.
func zoneBody(status, description string, vpcIDs []string) string {
	var fixture struct {
		Data struct {
			ID          string   `json:"hostedZoneId"`
			DomainName  string   `json:"domainName"`
			Status      string   `json:"status"`
			Type        string   `json:"type"`
			Description string   `json:"description"`
			VPCIDs      []string `json:"assocVpcIds"`
		} `json:"data"`
	}
	fixture.Data.ID = "zone-1"
	fixture.Data.DomainName = "<hostname>"
	fixture.Data.Status = status
	fixture.Data.Type = "PRIVATE"
	fixture.Data.Description = description
	fixture.Data.VPCIDs = vpcIDs
	b, err := json.Marshal(fixture)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// scriptedGets serves the strings in bodies, each a 200 response, to
// successive GET requests in order, repeating the last one once they run
// out, and delegates every other method to other.
func scriptedGets(t *testing.T, bodies []string, other http.HandlerFunc) http.Handler {
	t.Helper()
	var n atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			if other == nil {
				t.Fatalf("unexpected %s request", r.Method)
			}
			other(w, r)
			return
		}
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	})
}

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait, so a test exercising a wait's full bound runs in milliseconds
// rather than the real pollBound. The fake clock advances by exactly the
// duration each sleep call is asked to wait, so a wait's bound is still
// reached after the same number of iterations a real clock would take. It
// still reports ctx's own error from sleep, so a canceled-context test
// still behaves correctly.
func withInstantSleep(c *Client) *Client {
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
	return c
}

// --- CreateHostedZone ---

func TestCreateHostedZoneRequestBody(t *testing.T) {
	cases := []struct {
		name string
		in   *CreateHostedZoneInput
		want map[string]any
	}{
		{
			name: "with description",
			in: &CreateHostedZoneInput{
				DomainName:  "example.internal",
				VPCIDs:      []string{"vpc-1", "vpc-2"},
				Description: "my zone",
				NoWait:      true,
			},
			want: map[string]any{
				"domainName":  "example.internal",
				"assocVpcIds": []any{"vpc-1", "vpc-2"},
				"type":        "PRIVATE",
				"description": "my zone",
			},
		},
		{
			name: "without description",
			in: &CreateHostedZoneInput{
				DomainName: "example.internal",
				VPCIDs:     []string{"vpc-1"},
				NoWait:     true,
			},
			want: map[string]any{
				"domainName":  "example.internal",
				"assocVpcIds": []any{"vpc-1"},
				"type":        "PRIVATE",
				"description": "",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Fatalf("method = %s", r.Method)
				}
				if r.URL.Path != "/v1/dns/hosted-zone" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				body := decodeBody(t, r)
				if len(body) != len(tc.want) {
					t.Fatalf("body = %+v, want %+v", body, tc.want)
				}
				for k, v := range tc.want {
					got, ok := body[k]
					if !ok {
						t.Fatalf("body missing %q: %+v", k, body)
					}
					if list, isList := v.([]any); isList {
						gotList, _ := got.([]any)
						if len(gotList) != len(list) {
							t.Fatalf("body[%q] = %v, want %v", k, got, v)
						}
						for i := range list {
							if gotList[i] != list[i] {
								t.Fatalf("body[%q] = %v, want %v", k, got, v)
							}
						}
						continue
					}
					if got != v {
						t.Fatalf("body[%q] = %v, want %v", k, got, v)
					}
				}
				testutil.WriteFixture(t, w, "../testdata/dns/create_hosted_zone.json")
			}))

			out, err := client.CreateHostedZone(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("CreateHostedZone() error = %v", err)
			}
			if out.HostedZone.ID != "zone-1" {
				t.Fatalf("unexpected zone: %+v", out.HostedZone)
			}
		})
	}
}

// TestCreateHostedZoneDecodesFixture checks the sanitized raw fixture
// decodes with a CREATING status and assocVpcMapRegion, per the design's
// testing list.
func TestCreateHostedZoneDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/dns/create_hosted_zone.json")
	}))

	out, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	if err != nil {
		t.Fatalf("CreateHostedZone() error = %v", err)
	}
	if out.HostedZone.Status != StatusCreating {
		t.Fatalf("Status = %q, want %q", out.HostedZone.Status, StatusCreating)
	}
	if len(out.HostedZone.AssocVPCMapRegion) != 1 || out.HostedZone.AssocVPCMapRegion[0].Region != "hcm-3" {
		t.Fatalf("AssocVPCMapRegion = %+v", out.HostedZone.AssocVPCMapRegion)
	}
}

func TestCreateHostedZoneNoWaitReturnsCreatingResource(t *testing.T) {
	var getCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			getCalls.Add(1)
		}
		testutil.WriteFixture(t, w, "../testdata/dns/create_hosted_zone.json")
	}))

	out, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	if err != nil {
		t.Fatalf("CreateHostedZone() error = %v", err)
	}
	if out.HostedZone.Status != StatusCreating {
		t.Fatalf("Status = %q, want %q (the response's own status, no wait)", out.HostedZone.Status, StatusCreating)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GET calls = %d, want 0: NoWait must send no post-write read", getCalls.Load())
	}
}

func TestCreateHostedZoneRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	cases := []struct {
		name string
		in   *CreateHostedZoneInput
	}{
		{"nil input", nil},
		{"missing domain name", &CreateHostedZoneInput{VPCIDs: []string{"vpc-1"}}},
		{"missing vpc ids", &CreateHostedZoneInput{DomainName: "example.internal"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateHostedZone(context.Background(), tc.in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateHostedZoneMissingID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"domainName":"example.internal","status":"CREATING"}}`))
	}))

	_, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *vngcloud.APIError, got %v", err)
	}
	if apiErr.Message != "create response had no id" || apiErr.Operation != "dns.CreateHostedZone" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestCreateHostedZoneNotRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if vngcloud.IsRetryable(err) {
		t.Fatal("IsRetryable(err) = true, want false")
	}
}

func TestCreateHostedZoneVPCInactiveDNS(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"VPC: vpc-1 is inactive DNS."}`))
	}))

	_, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("IsNotFound(err) = false, err = %v", err)
	}
	if !strings.Contains(err.Error(), "inactive DNS") {
		t.Fatalf("err = %v, want a message naming the inactive VPC", err)
	}
}

// TestCreateHostedZoneAmbiguousErrorHintsAtListing checks that a create
// POST error that is not a 4xx *core.APIError, such as this 502, is
// wrapped with a hint to list zones before creating again, while errors.As
// can still reach the *core.APIError cause through it.
func TestCreateHostedZoneAmbiguousErrorHintsAtListing(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	_, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	if !strings.Contains(err.Error(), "list") {
		t.Fatalf("err = %v, want a hint to list before creating again", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("errors.As did not reach the *core.APIError cause: %v", err)
	}
}

func TestCreateHostedZoneConflict(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"domain already exists"}`))
	}))

	_, err := client.CreateHostedZone(context.Background(), &CreateHostedZoneInput{
		DomainName: "example.internal",
		VPCIDs:     []string{"vpc-1"},
		NoWait:     true,
	})
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict || apiErr.Code != "Conflict" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- UpdateHostedZone ---

func TestUpdateHostedZoneNoFieldsSet(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{HostedZoneID: "zone-1"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "requires at least one field") {
		t.Fatalf("err = %v, want a message naming at least one field to change", err)
	}
}

func TestUpdateHostedZoneMerge(t *testing.T) {
	cases := []struct {
		name string
		in   *UpdateHostedZoneInput
		want map[string]any
	}{
		{
			name: "description only resends the read VPC ids",
			in: &UpdateHostedZoneInput{
				HostedZoneID: "zone-1",
				Description:  vngcloud.Ptr("new description"),
				NoWait:       true,
			},
			want: map[string]any{
				"assocVpcIds": []any{"vpc-1", "vpc-2"},
				"description": "new description",
			},
		},
		{
			name: "vpc ids only resends the read description",
			in: &UpdateHostedZoneInput{
				HostedZoneID: "zone-1",
				VPCIDs:       vngcloud.Ptr([]string{"vpc-3"}),
				NoWait:       true,
			},
			want: map[string]any{
				"assocVpcIds": []any{"vpc-3"},
				"description": "old description",
			},
		},
		{
			name: "explicit empty VPCIDs sends []",
			in: &UpdateHostedZoneInput{
				HostedZoneID: "zone-1",
				VPCIDs:       vngcloud.Ptr([]string{}),
				NoWait:       true,
			},
			want: map[string]any{
				"assocVpcIds": []any{},
				"description": "old description",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var getCalls, putCalls atomic.Int64
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					n := getCalls.Add(1)
					if n == 1 {
						// The pre-write read: the zone as it stands before the update.
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(zoneBody(StatusActive, "old description", []string{"vpc-1", "vpc-2"})))
						return
					}
					// NoWait's single confirm read after the PUT.
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(zoneBody(StatusActive, "old description", []string{"vpc-1", "vpc-2"})))
				case http.MethodPut:
					putCalls.Add(1)
					body := decodeBody(t, r)
					for k, v := range tc.want {
						if list, isList := v.([]any); isList {
							gotList, _ := body[k].([]any)
							if len(gotList) != len(list) {
								t.Fatalf("body[%q] = %v, want %v", k, body[k], v)
							}
							for i := range list {
								if gotList[i] != list[i] {
									t.Fatalf("body[%q] = %v, want %v", k, body[k], v)
								}
							}
							continue
						}
						if body[k] != v {
							t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
						}
					}
					if len(body) != 2 {
						t.Fatalf("body = %+v, want exactly assocVpcIds and description", body)
					}
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
			}))

			if _, err := client.UpdateHostedZone(context.Background(), tc.in); err != nil {
				t.Fatalf("UpdateHostedZone() error = %v", err)
			}
			if putCalls.Load() != 1 {
				t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
			}
		})
	}
}

// TestUpdateHostedZoneNilVPCIDsNeverSendsEmptyList is the negative half of
// the merge test above: a nil VPCIDs, even when the zone's own current VPC
// list happens to be empty, must never turn into an empty read being
// confused with an explicit detach; both send []. This test instead checks
// the opposite direction: a non-empty read is resent unchanged, never [].
func TestUpdateHostedZoneNilVPCIDsResendsNonEmptyList(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "old", []string{"vpc-1", "vpc-2"})))
		case http.MethodPut:
			body := decodeBody(t, r)
			ids, _ := body["assocVpcIds"].([]any)
			if len(ids) != 2 {
				t.Fatalf("assocVpcIds = %v, want the 2 read ids, not []", ids)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("UpdateHostedZone() error = %v", err)
	}
}

func TestUpdateHostedZoneNoWaitSendsOneRead(t *testing.T) {
	var getCalls atomic.Int64
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPut:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
		NoWait:       true,
	})
	if err != nil {
		t.Fatalf("UpdateHostedZone() error = %v", err)
	}
	// One read before the write (the pre-write wait) and exactly one after
	// it (NoWait's own single confirm read): never a poll loop.
	if getCalls.Load() != 2 {
		t.Fatalf("GET calls = %d, want 2", getCalls.Load())
	}
}

func TestUpdateHostedZoneLockNotRetried(t *testing.T) {
	var putCalls atomic.Int64
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodPut:
			putCalls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"zone-1 was invalid status. Allowed in [ACTIVE, ERROR]"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	})))

	_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
		HostedZoneID: "zone-1",
		Description:  vngcloud.Ptr("new"),
		NoWait:       true,
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected error: %v", err)
	}
	if errors.Is(err, ErrZoneBusy) {
		t.Fatal("err wraps ErrZoneBusy, want the server's own 400 unwrapped")
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1: a 400 is never retried", putCalls.Load())
	}
}

func TestUpdateHostedZonePathIDRejection(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	for _, badID := range []string{"..", ".", "a/b", ""} {
		t.Run(badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateHostedZone(context.Background(), &UpdateHostedZoneInput{
				HostedZoneID: badID,
				Description:  vngcloud.Ptr("new"),
			})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// --- DeleteHostedZone ---

func TestDeleteHostedZoneRequestHasNoBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if len(data) != 0 {
				t.Fatalf("body = %s, want empty", data)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	_, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteHostedZone() error = %v", err)
	}
}

func TestDeleteHostedZoneNoWaitReturnsAtOnce(t *testing.T) {
	var getCallsAfterDelete atomic.Int64
	deleted := false
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if deleted {
				getCallsAfterDelete.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(zoneBody(StatusActive, "d", []string{"vpc-1"})))
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	out, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: "zone-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteHostedZone() error = %v", err)
	}
	if out == nil {
		t.Fatal("out is nil")
	}
	if getCallsAfterDelete.Load() != 0 {
		t.Fatalf("GET calls after delete = %d, want 0: NoWait must return at once", getCallsAfterDelete.Load())
	}
}

func TestDeleteHostedZonePathIDRejection(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	})
	for _, badID := range []string{"..", ".", "a/b", ""} {
		t.Run(badID, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.DeleteHostedZone(context.Background(), &DeleteHostedZoneInput{HostedZoneID: badID})
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
