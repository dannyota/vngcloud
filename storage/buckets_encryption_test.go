package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestCreateBucketEncryptionSequence(t *testing.T) {
	for _, scenario := range []string{"success", "existing", "false", "post lost", "enable refused", "enable lost", "read refused", "mismatch", "cancel before", "cancel confirm", "final read"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls []string
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				checkRegionHeaders(t, r, "<region-id-2>")
				if r.Method == "DELETE" {
					t.Error("unexpected delete")
				}
				body := okEnvelope
				switch {
				case r.Method == "POST":
					b, _ := io.ReadAll(r.Body)
					if string(b) != `{"status":"Disabled"}` {
						t.Errorf("body=%s", b)
					}
					if scenario == "post lost" {
						w.WriteHeader(502)
						return
					}
				case r.Method == "PUT":
					b, _ := io.ReadAll(r.Body)
					if string(b) != `{"enable":true}` {
						t.Errorf("body=%s", b)
					}
					if scenario == "enable refused" {
						body = `{"success":false,"code":403}`
					}
					if scenario == "enable lost" {
						w.WriteHeader(502)
						return
					}
				case r.URL.Path == encryptionPath:
					body = fixture(t, "get_bucket_encryption_true.json")
					if scenario == "mismatch" {
						body = fixture(t, "get_bucket_encryption_false.json")
					}
					if scenario == "read refused" {
						body = `{"success":false,"code":403}`
					}
				case r.URL.Path == detailsPath:
					body = fixture(t, "get_bucket_created.json")
					if scenario == "final read" {
						body = `{"success":false,"code":403}`
					}
				}
				_, _ = w.Write([]byte(body))
			})
			c := New(testutil.NewConfigWithCapture(t, h, func(cap transport.Capture) {
				if (scenario == "cancel before" && cap.Method == "POST") || (scenario == "cancel confirm" && cap.Method == "PUT") {
					cancel()
				}
			}))
			out, err := c.CreateBucket(ctx, &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", Encryption: scenario != "false"})
			want := []string{"POST " + bucketsPath, "PUT " + encryptionPath, "GET " + encryptionPath, "GET " + detailsPath}
			switch scenario {
			case "success", "existing", "false":
				if err != nil || out == nil {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				if scenario == "false" {
					want = []string{"POST " + bucketsPath, "GET " + detailsPath}
				}
			case "post lost":
				want = want[:1]
				if out != nil || err == nil || errors.Is(err, ErrBucketEncryptionIncomplete) || !strings.Contains(err.Error(), "GetBucket") {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			case "final read":
				if out != nil || !errors.Is(err, vngcloud.ErrPermission) || errors.Is(err, ErrBucketEncryptionIncomplete) || !strings.Contains(err.Error(), "encryption was confirmed") {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			default:
				if out != nil || !errors.Is(err, ErrBucketEncryptionIncomplete) {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				switch scenario {
				case "cancel before":
					want = want[:1]
				case "enable refused", "enable lost", "cancel confirm":
					want = want[:2]
				default:
					want = want[:3]
				}
				if strings.HasPrefix(scenario, "cancel") && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if (scenario == "enable refused" || scenario == "read refused") && !errors.Is(err, vngcloud.ErrPermission) {
					t.Fatal(err)
				}
				if scenario == "read refused" || scenario == "mismatch" || scenario == "cancel confirm" {
					if !errors.Is(err, ErrNotSettled) {
						t.Fatal(err)
					}
				}
				unknown := scenario == "enable lost" || scenario == "read refused" || scenario == "cancel confirm"
				text := "without encryption enabled by this call"
				if unknown {
					text = "encryption is unconfirmed"
				}
				if !strings.Contains(err.Error(), text) || !strings.Contains(err.Error(), "my-bucket") || !strings.Contains(err.Error(), "get-bucket-encryption") {
					t.Fatal(err)
				}
				if scenario == "enable refused" || scenario == "read refused" {
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.Code != "403" {
						t.Fatal(err)
					}
				}
			}
			if strings.Join(calls, ";") != strings.Join(want, ";") {
				t.Fatalf("calls=%v want=%v", calls, want)
			}
		})
	}
}

func TestCreateExistingBucketEncryption(t *testing.T) {
	enabled := false
	puts := 0
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			_, _ = w.Write([]byte(okEnvelope))
		case "PUT":
			puts++
			enabled = true
			_, _ = w.Write([]byte(okEnvelope))
		case "GET":
			if r.URL.Path == encryptionPath {
				if !enabled {
					t.Error("confirmation before enable")
				}
				_, _ = w.Write([]byte(fixture(t, "get_bucket_encryption_true.json")))
			} else {
				_, _ = w.Write([]byte(fixture(t, "get_bucket_created.json")))
			}
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	for _, encryption := range []bool{true, true, false} {
		out, err := c.CreateBucket(t.Context(), &CreateBucketInput{ProjectID: "proj-1", Bucket: "my-bucket", Encryption: encryption})
		if out == nil || err != nil {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	}
	if puts != 2 || !enabled {
		t.Fatalf("puts=%d enabled=%t", puts, enabled)
	}
}
