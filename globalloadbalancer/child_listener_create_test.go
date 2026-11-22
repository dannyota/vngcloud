package globalloadbalancer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const wantListenerBody = `{"name":"tcp-listener","protocol":"TCP","port":443,"globalPoolId":"gpool-1","allowedCidrs":"192.0.2.5/32,198.51.100.0/24","timeoutClient":50,"timeoutConnection":5,"timeoutMember":50}`

func readyFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.pool, f.group = true, true
	return f
}

func TestCreateListenerSendsExactRequest(t *testing.T) {
	for _, code := range []int{200, 202} {
		f := readyFixture(t)
		f.acceptStatus = code
		out, err := f.client(t).CreateListener(bg(), listenerInput())
		if err != nil || out == nil || out.Listener.ID != fxListener || out.Listener.AllowedCIDRs != fxCIDRs || out.Listener.GlobalPoolID != fxPool {
			t.Fatalf("code=%d out=%+v err=%v", code, out, err)
		}
		if f.writes() != 1 {
			t.Fatalf("calls=%v", f.calls)
		}
		sameJSON(t, f.body("POST "+glbPrefix+"/global-listeners"), wantListenerBody)
		last := f.calls[len(f.calls)-1]
		if strings.HasPrefix(last, "POST") {
			t.Fatalf("no confirmation read: %v", f.calls)
		}
	}
}

func TestCreateListenerRejectsInputBeforeRequests(t *testing.T) {
	for name, mutate := range map[string]func(*CreateListenerInput){
		"blank name":     func(in *CreateListenerInput) { in.Name = " " },
		"port zero":      func(in *CreateListenerInput) { in.Port = 0 },
		"port high":      func(in *CreateListenerInput) { in.Port = 65536 },
		"pool id":        func(in *CreateListenerInput) { in.PoolID = "../x" },
		"no cidrs":       func(in *CreateListenerInput) { in.AllowedCIDRs = nil },
		"slash zero":     func(in *CreateListenerInput) { in.AllowedCIDRs = []string{"0.0.0.0/0"} },
		"slash seven":    func(in *CreateListenerInput) { in.AllowedCIDRs = []string{"10.0.0.0/7"} },
		"host bits":      func(in *CreateListenerInput) { in.AllowedCIDRs = []string{"192.0.2.5/24"} },
		"ipv6":           func(in *CreateListenerInput) { in.AllowedCIDRs = []string{"2001:db8::/32"} },
		"duplicate":      func(in *CreateListenerInput) { in.AllowedCIDRs = []string{"192.0.2.5/32", "192.0.2.5/32"} },
		"joined":         func(in *CreateListenerInput) { in.AllowedCIDRs = []string{"192.0.2.5/32,198.51.100.0/24"} },
		"project":        func(in *CreateListenerInput) { in.BackendProjectID = "proj-2" },
		"server id":      func(in *CreateListenerInput) { in.BackendServerID = "a/b" },
		"missing server": func(in *CreateListenerInput) { in.BackendServerID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			f := readyFixture(t)
			in := listenerInput()
			mutate(in)
			out, err := f.client(t).CreateListener(bg(), in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) || out != nil || len(f.calls) != 0 {
				t.Fatalf("out=%v err=%v calls=%v", out, err, f.calls)
			}
		})
	}
}

func TestCreateListenerRefusesWhenChainIsNotExact(t *testing.T) {
	pools := "GET " + glbPrefix + "/global-pools"
	pool := "GET " + glbPrefix + "/global-pools/" + fxPool
	groups := pool + "/pool-members"
	group := groups + "/" + fxGroup
	base := readyFixture(t)
	for name, set := range map[string]struct{ key, body string }{
		"no pool":           {pools, `[]`},
		"two pools":         {pools, `[` + base.poolJSON() + `,{"id":"gpool-2"}]`},
		"other pool listed": {pools, `[{"id":"gpool-2"}]`},
		"pool not active":   {pool, strings.Replace(base.poolJSON(), `"status":"ACTIVE","health"`, `"status":"CREATING","health"`, 1)},
		"pool udp":          {pool, strings.Replace(base.poolJSON(), `"protocol":"TCP","status"`, `"protocol":"UDP","status"`, 1)},
		"pool algorithm":    {pool, strings.Replace(base.poolJSON(), `ROUND_ROBIN`, `LEAST`, 1)},
		"health differs":    {pool, strings.Replace(base.poolJSON(), `"intervalTime":30`, `"intervalTime":10`, 1)},
		"no group":          {groups, `[]`},
		"two groups":        {groups, `[` + base.groupJSON() + `,{"id":"gmem-2"}]`},
		"group public":      {group, strings.Replace(base.groupJSON(), `PRIVATE`, `PUBLIC`, 1)},
		"group not active":  {group, strings.Replace(base.groupJSON(), `"status":"OFFLINE","members"`, `"status":"ERROR","members"`, 1)},
		"group id":          {group, strings.Replace(base.groupJSON(), `"id":"gmem-1","name"`, `"id":"gmem-2","name"`, 1)},
		"row backup":        {group, strings.Replace(base.groupJSON(), `"backupRole":false`, `"backupRole":true`, 1)},
		"row count":         {group, strings.Replace(base.groupJSON(), `"members":[`, `"members":[{"id":"x"},`, 1)},
		"address moved":     {group, strings.Replace(base.groupJSON(), `10.1.2.3`, `10.1.2.9`, 1)},
		"existing listener": {"GET " + glbPrefix + "/global-listeners", `[{"id":"other"}]`},
		"server moved":      {"GET " + vsPrefix + "/servers/" + fxServer, `{"data":{"uuid":"srv-1","status":"ACTIVE","internalInterfaces":[{"serverUuid":"srv-1","networkUuid":"vpc-1","subnetUuid":"sub-1","fixedIp":"10.1.2.9"}]}}`},
		"glb layer 7":       {"GET " + glbPrefix, `{"id":"glb-1","package":"pkg-1","type":"Layer 7","status":"ACTIVE"}`},
	} {
		t.Run(name, func(t *testing.T) {
			f := readyFixture(t).on(set.key, jsonBody(set.body))
			out, err := f.client(t).CreateListener(bg(), listenerInput())
			if err == nil || out != nil || f.writes() != 0 || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v calls=%v", out, err, f.calls)
			}
		})
	}
	f := readyFixture(t)
	in := listenerInput()
	in.PoolID = "gpool-9"
	if _, err := f.client(t).CreateListener(bg(), in); err == nil || f.writes() != 0 {
		t.Fatalf("other pool err=%v", err)
	}
}

func TestCreateListenerDefinitiveAndAmbiguousOutcomes(t *testing.T) {
	post := "POST " + glbPrefix + "/global-listeners"
	for _, code := range []int{400, 403, 422, 429} {
		f := readyFixture(t).on(post, func(w httpResponseWriter, _ *httpRequest) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"cidr 192.0.2.5/32 rejected"}`))
		})
		_, err := f.client(t).CreateListener(bg(), listenerInput())
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.StatusCode != code || errors.Is(err, ErrNotSettled) || strings.Contains(err.Error(), "192.0.2.5") || f.count("POST ") != 1 {
			t.Fatalf("code=%d err=%v", code, err)
		}
	}
	for name, h := range map[string]httpHandlerFunc{
		"500":        status(500),
		"redirect":   func(w httpResponseWriter, _ *httpRequest) { w.Header().Set("Location", "/x"); w.WriteHeader(302) },
		"malformed":  jsonBody(`{`),
		"no id":      jsonBody(`{"globalPoolId":"gpool-1"}`),
		"bad id":     jsonBody(`{"id":"a b"}`),
		"duplicate":  jsonBody(`{"id":"glis-1","id":"glis-2"}`),
		"wrong glb":  jsonBody(`{"id":"glis-1","globalLoadBalancerId":"glb-9","globalPoolId":"gpool-1"}`),
		"wrong pool": jsonBody(`{"id":"glis-1","globalLoadBalancerId":"glb-1","globalPoolId":"gpool-9"}`),
		"secret":     jsonBody(`{"id":"glis-1","globalLoadBalancerId":"glb-1","globalPoolId":"gpool-1","x":"synthetic"}`),
	} {
		t.Run(name, func(t *testing.T) {
			f := readyFixture(t).on(post, h)
			_, err := f.client(t).CreateListener(bg(), listenerInput())
			if !errors.Is(err, ErrNotSettled) || f.count("POST ") != 1 || strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("err=%v calls=%v", err, f.calls)
			}
		})
	}
}

func TestCreateListenerConfirmation(t *testing.T) {
	get := "GET " + glbPrefix + "/global-listeners/" + fxListener
	base := readyFixture(t)
	base.listener = true
	for name, set := range map[string]struct{ body, want string }{
		"reordered ok":  {strings.Replace(base.listenerJSON(), fxCIDRs, "198.51.100.0/24,192.0.2.5/32", 1), ""},
		"widened":       {strings.Replace(base.listenerJSON(), fxCIDRs, "192.0.2.0/24,198.51.100.0/24", 1), "notsettled"},
		"extra":         {strings.Replace(base.listenerJSON(), fxCIDRs, fxCIDRs+",203.0.113.0/24", 1), "notsettled"},
		"wildcard":      {strings.Replace(base.listenerJSON(), fxCIDRs, "0.0.0.0/0", 1), "notsettled"},
		"missing":       {strings.Replace(base.listenerJSON(), fxCIDRs, "192.0.2.5/32", 1), "notsettled"},
		"port":          {strings.Replace(base.listenerJSON(), `"port":443`, `"port":444`, 1), "notsettled"},
		"timeout":       {strings.Replace(base.listenerJSON(), `"timeoutClient":50`, `"timeoutClient":60`, 1), "notsettled"},
		"pool":          {strings.Replace(base.listenerJSON(), `"globalPoolId":"gpool-1"`, `"globalPoolId":"gpool-2"`, 1), "notsettled"},
		"glb":           {strings.Replace(base.listenerJSON(), `"globalLoadBalancerId":"glb-1"`, `"globalLoadBalancerId":"glb-2"`, 1), "notsettled"},
		"protocol":      {strings.Replace(base.listenerJSON(), `"protocol":"TCP"`, `"protocol":"UDP"`, 1), "notsettled"},
		"name":          {strings.Replace(base.listenerJSON(), `"tcp-listener"`, `"x"`, 1), "notsettled"},
		"id":            {strings.Replace(base.listenerJSON(), `"id":"glis-1"`, `"id":"glis-2"`, 1), "notsettled"},
		"error state":   {strings.Replace(base.listenerJSON(), `"status":"ACTIVE"`, `"status":"ERROR"`, 1), "failed"},
		"unknown state": {strings.Replace(base.listenerJSON(), `"status":"ACTIVE"`, `"status":"WEIRD"`, 1), "notsettled"},
		"credential":    {strings.Replace(base.listenerJSON(), `"tcp-listener"`, `"x-synthetic"`, 1), "notsettled"},
	} {
		t.Run(name, func(t *testing.T) {
			f := readyFixture(t).on(get, jsonBody(set.body))
			out, err := f.client(t).CreateListener(bg(), listenerInput())
			switch set.want {
			case "":
				if err != nil || out == nil || out.Listener.ID != fxListener {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			case "failed":
				if !errors.Is(err, ErrFailed) || out == nil || out.Listener.ID != fxListener {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			default:
				if !errors.Is(err, ErrNotSettled) || out == nil || out.Listener.ID != fxListener || strings.Contains(err.Error(), "synthetic") || f.count("POST ") != 1 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
				if name == "credential" && out.Listener.Name != "" {
					t.Fatalf("leaked value kept: %+v", out.Listener)
				}
			}
		})
	}
}

func TestCreateListenerConfirmationScope(t *testing.T) {
	f := readyFixture(t)
	f.on("GET "+glbPrefix+"/global-listeners", func(w httpResponseWriter, _ *httpRequest) {
		if f.count("POST ") == 0 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_, _ = w.Write([]byte(`[` + f.listenerJSON() + `,{"id":"glis-2"}]`))
	})
	out, err := f.client(t).CreateListener(bg(), listenerInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Listener.ID != fxListener {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreateListenerWaitsAndCancels(t *testing.T) {
	f := readyFixture(t)
	f.listenerStatus = "CREATING"
	ctx, cancel := context.WithTimeout(bg(), 50*time.Millisecond)
	defer cancel()
	out, err := f.client(t).CreateListener(ctx, listenerInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Listener.ID != fxListener || f.count("POST ") != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}
