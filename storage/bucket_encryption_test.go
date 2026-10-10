package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const encryptionPath = bucketsPath + "/encryption"

func getEncryption(c *Client) (*GetBucketEncryptionOutput, error) {
	return c.GetBucketEncryption(context.Background(), &GetBucketEncryptionInput{ProjectID: "proj-1", BucketName: "my-bucket"})
}
func putEncryption(ctx context.Context, c *Client, enabled bool) (*PutBucketEncryptionOutput, error) {
	return c.PutBucketEncryption(ctx, &PutBucketEncryptionInput{ProjectID: "proj-1", BucketName: "my-bucket", Enabled: enabled})
}

func TestGetBucketEncryptionFixtures(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := &keyServer{status: 200, body: fixture(t, fmt.Sprintf("get_bucket_encryption_%t.json", enabled))}
		out, err := getEncryption(newTestClient(t, s.handler(t)))
		if err != nil || out.Enabled != enabled {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		if got := s.seen(); got.method != "GET" || got.path != encryptionPath {
			t.Fatalf("request=%+v", got)
		}
	}
}
func TestGetBucketEncryptionStrictBoolean(t *testing.T) {
	for _, data := range []string{"", `,"data":null`, `,"data":{}`, `,"data":{"encryption":null}`, `,"data":{"encryption":"true"}`, `,"data":{"encryption":1}`, `,"data":true`, `,"data":[]`, `,"data":{"Encryption":true}`} {
		s := &keyServer{status: 200, body: `{"success":true` + data + `}`}
		out, err := getEncryption(newTestClient(t, s.handler(t)))
		var api *vngcloud.APIError
		if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" {
			t.Fatalf("data=%s out=%+v err=%v", data, out, err)
		}
	}
}
func TestPutBucketEncryptionConfirmation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, result := range []string{"match", "mismatch", "permission", "invalid", "cancel"} {
			t.Run(fmt.Sprintf("%t/%s", enabled, result), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var methods []string
				c := New(testutil.NewConfigWithCapture(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
					checkRegionHeaders(t, r, "<region-id-2>")
					methods = append(methods, r.Method)
					if r.URL.Path != encryptionPath {
						t.Errorf("path=%s", r.URL.Path)
					}
					if r.Method == "PUT" {
						b, _ := io.ReadAll(r.Body)
						if string(b) != fmt.Sprintf(`{"enable":%t}`, enabled) {
							t.Errorf("body=%s", b)
						}
						_, _ = w.Write([]byte(fixture(t, "put_bucket_encryption.json")))
						return
					}
					state := enabled
					if result == "mismatch" {
						state = !enabled
					}
					body := fmt.Sprintf(`{"success":true,"data":{"encryption":%t}}`, state)
					if result == "permission" {
						body = `{"success":false,"code":403}`
					}
					if result == "invalid" {
						body = `{"success":true,"data":{}}`
					}
					_, _ = w.Write([]byte(body))
				}), func(capture transport.Capture) {
					if result == "cancel" && capture.Method == "PUT" {
						cancel()
					}
				}))
				out, err := putEncryption(ctx, c, enabled)
				if result == "match" {
					if err != nil || out == nil {
						t.Fatalf("out=%+v err=%v", out, err)
					}
				} else {
					if out != nil || !errors.Is(err, ErrNotSettled) || !strings.Contains(err.Error(), "my-bucket") || !strings.Contains(err.Error(), "GetBucketEncryption") {
						t.Fatalf("out=%+v err=%v", out, err)
					}
					if result == "permission" && !errors.Is(err, vngcloud.ErrPermission) {
						t.Fatal(err)
					}
					if result == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
				want := "PUT GET"
				if result == "cancel" {
					want = "PUT"
				}
				if strings.Join(methods, " ") != want {
					t.Fatalf("methods=%v", methods)
				}
			})
		}
	}
}
func TestBucketEncryptionValidation(t *testing.T) {
	for _, v := range []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb"} {
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
		for _, identifiers := range [][2]string{{v, "my-bucket"}, {"proj-1", v}} {
			_, e1 := c.GetBucketEncryption(t.Context(), &GetBucketEncryptionInput{ProjectID: identifiers[0], BucketName: identifiers[1]})
			_, e2 := c.PutBucketEncryption(t.Context(), &PutBucketEncryptionInput{ProjectID: identifiers[0], BucketName: identifiers[1]})
			for _, err := range []error{e1, e2} {
				if !errors.Is(err, vngcloud.ErrInvalidInput) {
					t.Fatal(err)
				}
			}
		}
	}
	c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
	_, e1 := c.GetBucketEncryption(t.Context(), nil)
	_, e2 := c.PutBucketEncryption(t.Context(), nil)
	if !errors.Is(e1, vngcloud.ErrInvalidInput) || !errors.Is(e2, vngcloud.ErrInvalidInput) {
		t.Fatalf("%v %v", e1, e2)
	}
}
func TestBucketEncryptionRegion(t *testing.T) {
	n := 0
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		checkRegionHeaders(t, r, "<region-id-1>")
		_, _ = w.Write([]byte(`{"success":true,"data":{"encryption":false}}`))
	}))
	_, err := c.GetBucketEncryption(t.Context(), &GetBucketEncryptionInput{Region: "han02", ProjectID: "proj-1", BucketName: "my-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.PutBucketEncryption(t.Context(), &PutBucketEncryptionInput{Region: "han02", ProjectID: "proj-1", BucketName: "my-bucket"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("requests=%d", n)
	}
}
func TestBucketEncryptionErrorsAndMissing(t *testing.T) {
	for _, put := range []bool{false, true} {
		run := func(c *Client) error {
			if put {
				_, err := putEncryption(t.Context(), c, true)
				return err
			}
			_, err := getEncryption(c)
			return err
		}
		for _, tt := range statusCases {
			checkStatusCase(t, tt, func(body string, status int) error {
				return run(newTestClient(t, (&keyServer{status: status, body: body}).handler(t)))
			}, `{"success":true,"data":{"encryption":true}}`)
		}
		for _, code := range []int{403, 404, 112, 114} {
			err := run(newTestClient(t, (&keyServer{status: 200, body: fmt.Sprintf(`{"success":false,"code":%d}`, code)}).handler(t)))
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.Code != strconv.Itoa(code) {
				t.Fatal(err)
			}
			if code == 403 && !errors.Is(err, vngcloud.ErrPermission) {
				t.Fatal(err)
			}
		}
		for _, missing := range []bool{false, true} {
			reads := 0
			c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == detailsPath {
					reads++
					body := fixture(t, "get_bucket.json")
					if missing {
						body = fixture(t, "error_envelope_not_found.json")
					}
					_, _ = w.Write([]byte(body))
				}
			}))
			err := run(c)
			var api *vngcloud.APIError
			if reads != 1 || !errors.As(err, &api) {
				t.Fatalf("reads=%d err=%v", reads, err)
			}
			if missing {
				if !errors.Is(err, vngcloud.ErrNotFound) {
					t.Fatal(err)
				}
			} else if api.Code != "EmptyResponse" {
				t.Fatal(err)
			}
		}
	}
}
func TestPutBucketEncryptionNeverResends(t *testing.T) {
	for _, status := range []int{401, 429, 502, 307} {
		calls := 0
		c := New(testutil.NewRetryConfig(t, serve(t, func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(status) })))
		out, err := putEncryption(t.Context(), c, true)
		if out != nil || err == nil || calls != 1 {
			t.Fatalf("status=%d calls=%d out=%+v err=%v", status, calls, out, err)
		}
	}
}

func TestEncryptionEmptyFallbackPreservesOriginalError(t *testing.T) {
	for _, put := range []bool{false, true} {
		for _, detail := range []string{"", `{"success":false,"code":403}`} {
			reads := 0
			c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == detailsPath {
					reads++
					_, _ = w.Write([]byte(detail))
				}
			}))
			var err error
			if put {
				_, err = putEncryption(t.Context(), c, false)
			} else {
				_, err = getEncryption(c)
			}
			var api *vngcloud.APIError
			if reads != 1 || !errors.As(err, &api) || api.Code != "EmptyResponse" || errors.Is(err, vngcloud.ErrPermission) {
				t.Fatalf("reads=%d err=%v", reads, err)
			}
		}
	}
}

func TestPutBucketEncryptionLostResponseDoesNotRead(t *testing.T) {
	calls := 0
	c := New(roundTripConfig(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "PUT" {
			t.Errorf("unexpected method %s", r.Method)
		}
		return nil, io.ErrUnexpectedEOF
	})))
	c.regionIDs = map[string]string{"HCM04": "<region-id-2>"}
	out, err := putEncryption(t.Context(), c, true)
	if out != nil || err == nil || calls != 1 || !strings.Contains(err.Error(), "may have happened") {
		t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
	}
}

func TestPutBucketEncryptionFailedDialIsKnownUnsent(t *testing.T) {
	c := New(roundTripConfig(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}
	})))
	c.regionIDs = map[string]string{"HCM04": "<region-id-2>"}
	out, err := putEncryption(t.Context(), c, true)
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) || !api.Retryable || strings.Contains(err.Error(), "may have happened") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	partial := incompleteBucketEncryption("my-bucket", err)
	if !errors.Is(partial, ErrBucketEncryptionIncomplete) || !strings.Contains(partial.Error(), "without encryption enabled by this call") {
		t.Fatal(partial)
	}
}

func TestPutBucketEncryptionUnexpectedSuccessIsUncertain(t *testing.T) {
	c := newTestClient(t, (&keyServer{status: 202, body: okEnvelope}).handler(t))
	out, err := putEncryption(t.Context(), c, true)
	if out != nil || err == nil || !strings.Contains(err.Error(), "may have happened") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
