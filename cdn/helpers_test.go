package cdn

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// reqLog records every request a test server sees.
type reqLog struct {
	mu    sync.Mutex
	lines []string
	body  map[string]string
}

func newReqLog() *reqLog { return &reqLog{body: map[string]string{}} }

// wrap records "METHOD path" and the body of each request, then calls h.
func (l *reqLog) wrap(h func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		line := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/vcdn-api/v1/")
		l.mu.Lock()
		l.lines = append(l.lines, line)
		l.body[line] = string(b)
		l.mu.Unlock()
		h(w, r)
	}
}

func (l *reqLog) count(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

func (l *reqLog) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

// updateBodyText is the body of the last update request.
func (l *reqLog) updateBodyText() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.body["PUT cdn/update"]
}

// fakeClock is the settle wait's clock: sleeping advances it.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (f *fakeClock) install(c *Client) {
	f.now = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	c.now = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.now = f.now.Add(d)
		f.sleeps = append(f.sleeps, d)
		return nil
	}
}

func (f *fakeClock) slept() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	var total time.Duration
	for _, d := range f.sleeps {
		total += d
	}
	return total
}

// detailWithStatus returns the detail fixture with its status replaced.
func detailWithStatus(t *testing.T, status int) string {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFixture(t, "webaccelerator-detail.json")), &env); err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(env["data"], &data); err != nil {
		t.Fatal(err)
	}
	data["status"], _ = json.Marshal(status)
	env["data"], _ = json.Marshal(data)
	b, _ := json.Marshal(env)
	return string(b)
}

const (
	okEnvelope   = `{"success":true,"code":200,"message":"Update CDN successful, your CDN domain will effect after 5 minutes.","data":""}`
	busyEnvelope = `{"success":false,"code":500,"message":"Current cdn status is not allow to update or delete","data":""}`
)

func jsonReply(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// sim is a fake CDN: detail reads return its status, and a write replaces the
// status with seq, one entry per later read, the last sticking.
type sim struct {
	t      *testing.T
	mu     sync.Mutex
	status int
	seq    []int
	wrote  bool
	log    *reqLog
	// writeBody answers the write; okEnvelope when empty.
	writeBody string
	// writeHTTP answers the write with this status when set.
	writeHTTP int
	// detailOverride, when set, answers a detail read instead of the status.
	detailOverride func(n int) (string, bool)
	reads          int
}

func newSim(t *testing.T, status int, seq ...int) *sim {
	return &sim{t: t, status: status, seq: seq, log: newReqLog()}
}

func (s *sim) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/vcdn-api/v1/cdn/detail/"):
		s.reads++
		if s.detailOverride != nil {
			if body, ok := s.detailOverride(s.reads); ok {
				jsonReply(w, body)
				return
			}
		}
		st := s.status
		if s.wrote && len(s.seq) > 0 {
			st = s.seq[0]
			if len(s.seq) > 1 {
				s.seq = s.seq[1:]
			}
		}
		jsonReply(w, detailWithStatus(s.t, st))
	default:
		s.wrote = true
		if s.writeHTTP != 0 {
			w.WriteHeader(s.writeHTTP)
			return
		}
		body := s.writeBody
		if body == "" {
			body = okEnvelope
		}
		jsonReply(w, body)
	}
}

// harness builds a client on the sim with a fake clock.
func (s *sim) harness(t *testing.T) (*vcdnHarness, *fakeClock) {
	h := newVCDN(t, testKey, s.log.wrap(s.handler))
	clock := &fakeClock{}
	clock.install(h.Client)
	return h, clock
}
