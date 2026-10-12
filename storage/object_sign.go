package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

func (c *ObjectClient) signObjectRequest(req *http.Request, payloadHash string, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Del("Authorization")
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	values := map[string][]string{"host": {host}}
	for name, entries := range req.Header {
		lower := strings.ToLower(name)
		if lower == "host" || lower == "authorization" {
			continue
		}
		for _, value := range entries {
			values[lower] = append(values[lower], strings.Join(strings.Fields(value), " "))
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var headers strings.Builder
	for _, name := range names {
		headers.WriteString(name + ":" + strings.Join(values[name], ",") + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{req.Method, objectEncodePath(req.URL.Path), "", headers.String(), signed, payloadHash}, "\n")
	scope := stamp[:8] + "/" + c.region + "/s3/aws4_request"
	digest := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hex.EncodeToString(digest[:])
	key := []byte("AWS4" + c.credentials.SecretAccessKey.Reveal())
	for _, part := range []string{stamp[:8], c.region, "s3", "aws4_request"} {
		key = objectHMAC(key, part)
	}
	signature := hex.EncodeToString(objectHMAC(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.credentials.AccessKeyID.Reveal()+"/"+scope+
		", SignedHeaders="+signed+", Signature="+signature)
}

func objectHMAC(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
