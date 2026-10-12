package storage

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"danny.vn/vngcloud"
)

// MaxPutObjectSize is the single-PUT byte limit, not a verified server limit.
const MaxPutObjectSize int64 = 5_000_000_000

// DefaultMaxDownloadBytes bounds downloads unless the caller sets a limit.
const DefaultMaxDownloadBytes int64 = 5_000_000_000

// ObjectCredentials holds an explicit S3 credential pair, separate from IAM.
type ObjectCredentials struct {
	AccessKeyID     vngcloud.Secret
	SecretAccessKey vngcloud.Secret
}

// ObjectConfig selects a fixed S3 region and an operation timeout. Zero
// Timeout selects five minutes; positive values cannot exceed one hour.
type ObjectConfig struct {
	Region      string
	Credentials ObjectCredentials
	Timeout     time.Duration
}

// ObjectClient owns native S3 traffic independently of the management client.
// A constructed client is safe for concurrent calls.
type ObjectClient struct {
	httpClient  *http.Client
	region      string
	credentials ObjectCredentials
	timeout     time.Duration
	now         func() time.Time
}

// NewObjectClient validates cfg locally and builds a trusted S3 transport.
func NewObjectClient(cfg ObjectConfig) (*ObjectClient, error) {
	if cfg.Credentials.AccessKeyID.Reveal() == "" || cfg.Credentials.SecretAccessKey.Reveal() == "" {
		return nil, fmt.Errorf("%w: S3 access ID and secret are required", vngcloud.ErrNoCredentials)
	}
	if objectHasControls(cfg.Credentials.AccessKeyID.Reveal()) || objectHasControls(cfg.Credentials.SecretAccessKey.Reveal()) {
		return nil, fmt.Errorf("%w: S3 credentials contain control bytes", vngcloud.ErrInvalidConfig)
	}
	if cfg.Region != "HCM04" && cfg.Region != "HAN02" {
		return nil, fmt.Errorf("%w: S3 region must be HCM04 or HAN02", vngcloud.ErrInvalidConfig)
	}
	if cfg.Timeout < 0 || cfg.Timeout > time.Hour {
		return nil, fmt.Errorf("%w: S3 timeout must be zero or positive and at most one hour", vngcloud.ErrInvalidConfig)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	tr := &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		DisableCompression:     true,
		DisableKeepAlives:      true,
		Protocols:              protocols,
		MaxResponseHeaderBytes: 32 * 1024,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
	}
	return &ObjectClient{
		httpClient: &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		region:     cfg.Region, credentials: cfg.Credentials, timeout: timeout, now: time.Now,
	}, nil
}

// Format hides private transport and credential fields, including under %#v.
func (ObjectClient) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("storage.ObjectClient{[redacted]}"))
}

// MarshalJSON exposes no transport, credential, or clock internals.
func (ObjectClient) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }

// LogValue hides client internals from structured logs.
func (ObjectClient) LogValue() slog.Value {
	return slog.StringValue("storage.ObjectClient{[redacted]}")
}

func (c *ObjectClient) objectURL(bucket, key string) (*url.URL, error) {
	if c == nil || !bucketNamePattern.MatchString(bucket) || key == "" || !utf8.ValidString(key) || len(key) > 1024 {
		return nil, fmt.Errorf("%w: valid bucket and UTF-8 object key of at most 1024 bytes are required", vngcloud.ErrInvalidInput)
	}
	path := "/" + bucket + "/" + key
	return &url.URL{Scheme: "https", Host: strings.ToLower(c.region) + ".vstorage.vngcloud.vn", Path: path, RawPath: objectEncodePath(path)}, nil
}

func (c *ObjectClient) objectBucketURL(bucket string) (*url.URL, error) {
	if c == nil || !bucketNamePattern.MatchString(bucket) {
		return nil, fmt.Errorf("%w: valid bucket is required", vngcloud.ErrInvalidInput)
	}
	path := "/" + bucket
	return &url.URL{Scheme: "https", Host: strings.ToLower(c.region) + ".vstorage.vngcloud.vn", Path: path, RawPath: objectEncodePath(path)}, nil
}

func objectHasControls(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] == 0x7f {
			return true
		}
	}
	return false
}

func objectEncodePath(path string) string {
	const hex = "0123456789ABCDEF"
	var escaped strings.Builder
	for i := 0; i < len(path); i++ {
		b := path[i]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '-', b == '_', b == '.', b == '~', b == '/':
			escaped.WriteByte(b)
		default:
			escaped.WriteByte('%')
			escaped.WriteByte(hex[b>>4])
			escaped.WriteByte(hex[b&15])
		}
	}
	return escaped.String()
}
