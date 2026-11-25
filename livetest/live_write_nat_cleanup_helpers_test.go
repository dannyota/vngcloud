//go:build livewrite

package livetest_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"danny.vn/vngcloud/internal/jsonresponse"
)

type natDeleteTransport struct {
	base     http.RoundTripper
	natID    *string
	sent     atomic.Bool
	posts    atomic.Int32
	count    atomic.Int32
	accepted atomic.Bool
}

func (t *natDeleteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/nats") {
		t.posts.Add(1)
	}
	if req.Method != http.MethodDelete || *t.natID == "" || !strings.HasSuffix(req.URL.Path, "/nats/"+*t.natID) {
		return t.base.RoundTrip(req)
	}
	t.sent.Store(true)
	t.count.Add(1)
	res, err := t.base.RoundTrip(req)
	if err == nil && res != nil && res.Body != nil {
		res.Body = &natDeleteBody{ReadCloser: res.Body, status: res.StatusCode, transport: t}
	}
	return res, err
}

func (t *natDeleteTransport) valid() bool { return t.count.Load() == 1 && t.accepted.Load() }

type natDeleteBody struct {
	io.ReadCloser
	status    int
	transport *natDeleteTransport
	data      []byte
	overflow  bool
}

func (b *natDeleteBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if len(b.data)+n > 1<<20 {
		b.overflow = true
	} else if !b.overflow {
		b.data = append(b.data, p[:n]...)
	}
	if err == io.EOF && !b.overflow && b.status == http.StatusOK {
		var result struct {
			Code    *int  `json:"code"`
			Success *bool `json:"success"`
		}
		if jsonresponse.ValidateStrict(b.data, &result) == nil && json.Unmarshal(b.data, &result) == nil && result.Code != nil && *result.Code == 0 && result.Success != nil && *result.Success {
			b.transport.accepted.Store(true)
		}
	}
	return n, err
}

func natOrderNotSent(err error, posts int32) bool {
	return err != nil && posts == 0
}

func natVPCDeleteAllowed(attempted, natAbsent, billingAbsent, routesRestored bool) bool {
	return !attempted || (natAbsent && billingAbsent && routesRestored)
}
