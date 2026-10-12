package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

// The GET and PUT vectors come from AWS's published S3 SigV4 examples:
// https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
// These public example credentials are fixtures, not endpoint configuration.
func TestObjectSignKnownVectors(t *testing.T) {
	c := objectTestClient(t)
	c.region = "us-east-1"
	c.credentials = ObjectCredentials{AccessKeyID: vngcloud.Secret("AKIAIOSFODNN7EXAMPLE"), SecretAccessKey: vngcloud.Secret("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")}
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	for _, tc := range []struct {
		method, path, hash, headers, canonicalHash, signature string
		extra                                                 map[string]string
	}{
		{"GET", "/test.txt", emptyHash, "host;range;x-amz-content-sha256;x-amz-date",
			"7344ae5b7ee6c3e7e6b0fe0640412a37625d1fbfff95c48bbb2dc43964946972",
			"f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41", map[string]string{"Range": "bytes=0-9"}},
		{"PUT", "/test%24file.text", "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072",
			"date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
			"9e0e90d9c76de8fa5b200d8c849cd5b8dc7a3be3951ddb7f6a76b4158342019d",
			"98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
			map[string]string{"Date": "Fri, 24 May 2013 00:00:00 GMT", "X-Amz-Storage-Class": "REDUCED_REDUNDANCY"}},
	} {
		t.Run(tc.method, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tc.method, "https://examplebucket.s3.amazonaws.com"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			for name, value := range tc.extra {
				req.Header.Set(name, value)
			}
			now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
			reference := req.Clone(t.Context())
			canonical, toSign, authorization := objectReferenceSign(reference, tc.hash, now, c.region, c.credentials)
			if objectTestHash(canonical) != tc.canonicalHash {
				t.Fatalf("canonical hash=%s", objectTestHash(canonical))
			}
			wantToSign := "AWS4-HMAC-SHA256\n20130524T000000Z\n20130524/us-east-1/s3/aws4_request\n" + tc.canonicalHash
			if toSign != wantToSign {
				t.Fatal("wrong reference string to sign")
			}
			want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=" + tc.headers + ", Signature=" + tc.signature
			if authorization != want {
				t.Fatal("reference does not match published vector")
			}
			c.signObjectRequest(req, tc.hash, now)
			if req.Header.Get("Authorization") != want {
				t.Fatalf("Authorization=%s", req.Header.Get("Authorization"))
			}
		})
	}
}

// This reference checks simple supported requests independently of the native
// helper. Agreement here is local evidence, not a published signature vector.
func objectReferenceSign(req *http.Request, hash string, now time.Time, region string, credentials ObjectCredentials) (string, string, string) {
	stamp := now.UTC().Format("20060102T150405Z")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", hash)
	names := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	if req.Host != "" {
		values["host"] = req.Host
	}
	for name, entries := range req.Header {
		if strings.EqualFold(name, "Authorization") {
			continue
		}
		name = strings.ToLower(name)
		names = append(names, name)
		cleaned := make([]string, len(entries))
		for i, value := range entries {
			cleaned[i] = strings.Join(strings.Fields(value), " ")
		}
		values[name] = strings.Join(cleaned, ",")
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		lines = append(lines, name+":"+values[name])
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, strings.Join(lines, "\n") + "\n", signed, hash}, "\n")
	scope := stamp[:8] + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + objectTestHash(canonical)
	key := []byte("AWS4" + credentials.SecretAccessKey.Reveal())
	for _, part := range []string{stamp[:8], region, "s3", "aws4_request"} {
		key = objectTestMAC(key, part)
	}
	auth := "AWS4-HMAC-SHA256 Credential=" + credentials.AccessKeyID.Reveal() + "/" + scope + ", SignedHeaders=" + signed + ", Signature=" + hex.EncodeToString(objectTestMAC(key, toSign))
	return canonical, toSign, auth
}
func objectTestHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
func objectTestMAC(key []byte, value string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(value))
	return m.Sum(nil)
}

func TestObjectSignSupportedRequests(t *testing.T) {
	c := objectTestClient(t)
	for _, region := range []string{"HCM04", "HAN02"} {
		c.region = region
		for _, hour := range []int{0, 23} {
			u, err := c.objectURL("bucket", "/a//../é%2F +?#")
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, u.String(), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "example.test"
			req.Header.Set("Content-Type", " application/octet-stream ")
			req.Header.Set("If-None-Match", "*")
			req.Header["X-Amz-Meta-Test"] = []string{"  a\t b  ", " c   d "}
			req.Header.Set("Authorization", "old-authorization")
			now := time.Date(2025, 1, 2, hour, 30, 0, 0, time.FixedZone("offset", 3600))
			reference := req.Clone(t.Context())
			_, _, want := objectReferenceSign(reference, objectTestHash(""), now, region, c.credentials)
			c.signObjectRequest(req, objectTestHash(""), now)
			if req.Header.Get("Authorization") != want || req.Header.Get("X-Amz-Date") != now.UTC().Format("20060102T150405Z") {
				t.Fatal("signature differs from reference")
			}
			c.signObjectRequest(req, objectTestHash(""), now)
			if req.Header.Get("Authorization") != want {
				t.Fatal("resigning included old authorization")
			}
		}
	}
}

// This bucket-list vector is from the same published AWS SigV4 examples.
func TestObjectQuerySignPublishedVector(t *testing.T) {
	c := objectTestClient(t)
	c.region = "us-east-1"
	c.credentials = ObjectCredentials{AccessKeyID: vngcloud.Secret("AKIAIOSFODNN7EXAMPLE"), SecretAccessKey: vngcloud.Secret("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	canonical, _, want := objectReferenceSign(req.Clone(t.Context()), objectTestHash(""), now, c.region, c.credentials)
	if objectTestHash(canonical) != "df57d21db20da04d7fa30298dd4488ba3a2b47ca3a489c74750e0f1e7df1b9b7" {
		t.Fatal("reference canonical hash differs from published vector")
	}
	published := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
	if want != published {
		t.Fatal("reference signature differs from published vector")
	}
	c.signObjectRequest(req, objectTestHash(""), now)
	if req.Header.Get("Authorization") != published {
		t.Fatal("signer ignores the published canonical query")
	}
	if req.URL.RawQuery != "max-keys=2&prefix=J" {
		t.Fatal("signer changed wire query")
	}
}

func TestObjectQuerySignEncoding(t *testing.T) {
	values := url.Values{
		"prefix": {"é/猫 %2F+/?#&="}, "delimiter": {"/"},
		"continuation-token": {"a+b c%2F/=&?#"}, "empty": {""},
		"z": {"z", "/", "+", " "}, "é": {"~"}, "a/": {"value"}, "a?": {"value"},
	}
	before := url.Values{}
	for name, entries := range values {
		before[name] = append([]string(nil), entries...)
	}
	const want = "%C3%A9=~&a%2F=value&a%3F=value&continuation-token=a%2Bb%20c%252F%2F%3D%26%3F%23&delimiter=%2F&empty=&prefix=%C3%A9%2F%E7%8C%AB%20%252F%2B%2F%3F%23%26%3D&z=%20&z=%2B&z=%2F&z=z"
	if got := objectEncodeQuery(values); got != want {
		t.Fatalf("query=%s want=%s", got, want)
	}
	if !reflect.DeepEqual(values, before) {
		t.Fatal("query encoding mutated caller values")
	}
	if objectEncodeQuery(nil) != "" || objectEncodeQuery(url.Values{"absent": nil}) != "" {
		t.Fatal("empty query encoded")
	}
	c := objectTestClient(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/bucket?"+want {
			t.Errorf("RequestURI=%s", r.RequestURI)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, region := range []string{"HCM04", "HAN02"} {
		c.region = region
		for _, hour := range []int{0, 23} {
			u, err := c.objectBucketURL("bucket")
			if err != nil {
				t.Fatal(err)
			}
			u.RawQuery = objectEncodeQuery(values)
			u.Scheme = "http"
			u.Host = strings.TrimPrefix(server.URL, "http://")
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u.String(), nil)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2025, 1, 2, hour, 30, 0, 0, time.FixedZone("offset", 3600))
			_, _, auth := objectReferenceSign(req.Clone(t.Context()), objectTestHash(""), now, region, c.credentials)
			c.signObjectRequest(req, objectTestHash(""), now)
			if req.Header.Get("Authorization") != auth || req.Header.Get("X-Amz-Date") != now.UTC().Format("20060102T150405Z") {
				t.Fatal("query signature or UTC date differs from reference")
			}
			c.signObjectRequest(req, objectTestHash(""), now)
			if req.Header.Get("Authorization") != auth || req.URL.RawQuery != want {
				t.Fatal("resigning changed signature or query")
			}
			resp, err := c.httpClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, resp.Body)
			closeErr := resp.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("read=%v close=%v", readErr, closeErr)
			}
		}
	}
}

func TestObjectQuerySignExactRawQuery(t *testing.T) {
	c := objectTestClient(t)
	for _, query := range []string{"z=last&a=%20", "a=+", "a=%2f", "empty="} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test/bucket?"+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
		_, _, want := objectReferenceSign(req.Clone(t.Context()), objectTestHash(""), now, c.region, c.credentials)
		c.signObjectRequest(req, objectTestHash(""), now)
		if req.Header.Get("Authorization") != want || req.URL.RawQuery != query {
			t.Fatal("signer reencoded RawQuery")
		}
	}
}
