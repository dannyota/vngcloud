package vngcloud_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

// s3Signer signs requests with AWS Signature Version 4 for the s3 service,
// using only the standard library. It signs the host header, the two x-amz
// headers it sets, and every other header already on the request. The path is
// not double-encoded, as S3 requires.
type s3Signer struct {
	accessKey string
	secretKey string
	region    string
}

func (s s3Signer) sign(req *http.Request, payload []byte, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	day := stamp[:8]
	payloadHash := hexSHA256(payload)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("X-Amz-Date", stamp)

	signed := []string{"host"}
	values := map[string]string{"host": req.URL.Host}
	for name, vals := range req.Header {
		lower := strings.ToLower(name)
		clean := make([]string, len(vals))
		for i, v := range vals {
			clean[i] = strings.Join(strings.Fields(v), " ")
		}
		values[lower] = strings.Join(clean, ",")
		signed = append(signed, lower)
	}
	sort.Strings(signed)
	var headers strings.Builder
	for _, name := range signed {
		headers.WriteString(name + ":" + values[name] + "\n")
	}
	signedList := strings.Join(signed, ";")

	canonical := strings.Join([]string{
		req.Method,
		s3EncodePath(req.URL.Path),
		canonicalQuery(req.URL.Query()),
		headers.String(),
		signedList,
		payloadHash,
	}, "\n")

	scope := day + "/" + s.region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hexSHA256([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+s.secretKey), day)
	key = hmacSHA256(key, s.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.accessKey+"/"+scope+
		", SignedHeaders="+signedList+", Signature="+signature)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

// uriEncode percent-encodes everything except the RFC 3986 unreserved
// characters, with upper-case hex.
func uriEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func s3EncodePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = uriEncode(p)
	}
	return strings.Join(parts, "/")
}

func canonicalQuery(q url.Values) string {
	var pairs []string
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, uriEncode(k)+"="+uriEncode(v))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// TestS3SignerKnownVector checks the signer against the GET Object example in
// the Amazon S3 documentation for Signature Version 4.
func TestS3SignerKnownVector(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")
	signer := s3Signer{accessKey: "AKIAIOSFODNN7EXAMPLE", secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", region: "us-east-1"}
	signer.sign(req, nil, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
}
