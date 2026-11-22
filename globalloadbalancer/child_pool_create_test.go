package globalloadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const wantPoolBody = `{"name":"tcp-pool","protocol":"TCP","algorithm":"ROUND_ROBIN","health":{"protocol":"TCP","healthyThreshold":3,"unhealthyThreshold":3,"interval":30,"timeout":5},"globalPoolMembers":[{"name":"tcp-backend","region":"hcm","type":"PRIVATE","vpcId":"vpc-1","trafficDial":100,"members":[{"name":"tcp-backend","address":"10.1.2.3","subnetId":"sub-1","port":8080,"monitorPort":8080,"weight":1,"backupRole":false}]}]}`

func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("body %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestCreatePoolSendsExactRequest(t *testing.T) {
	for _, code := range []int{200, 202} {
		f := newFixture(t)
		f.acceptStatus = code
		out, err := f.client(t).CreatePool(bg(), poolInput())
		if err != nil || out == nil || out.Pool.ID != fxPool || out.PoolMember.ID != fxGroup || len(out.PoolMember.Members) != 1 || out.PoolMember.Members[0].ID != fxRow || out.Pool.Status != "ACTIVE" {
			t.Fatalf("code=%d out=%+v err=%v", code, out, err)
		}
		if f.count("POST ") != 1 || f.count("DELETE ") != 0 || f.count("POST "+glbPrefix+"/global-pools") != 1 {
			t.Fatalf("calls=%v", f.calls)
		}
		sameJSON(t, f.body("POST "+glbPrefix+"/global-pools"), wantPoolBody)
		first := -1
		for i, c := range f.calls {
			if strings.HasPrefix(c, "POST ") {
				first = i
			}
		}
		for _, guard := range []string{"GET " + glbPrefix, "GET /v1/regions", "GET /v1/packages", "GET " + vsPrefix + "/servers/" + fxServer, "GET " + vsPrefix + "/networks/" + fxVPC, "GET " + vsPrefix + "/networks/" + fxVPC + "/subnets/" + fxSubnet, "GET " + glbPrefix + "/global-pools", "GET " + glbPrefix + "/global-listeners"} {
			seen := false
			for _, c := range f.calls[:first] {
				seen = seen || c == guard
			}
			if !seen {
				t.Errorf("guard %q not read before POST: %v", guard, f.calls)
			}
		}
	}
}

func TestCreatePoolRejectsInputBeforeRequests(t *testing.T) {
	for name, mutate := range map[string]func(*CreatePoolInput){
		"blank name":        func(in *CreatePoolInput) { in.Name = "  " },
		"blank member":      func(in *CreatePoolInput) { in.MemberName = "\t" },
		"port zero":         func(in *CreatePoolInput) { in.BackendPort = 0 },
		"port high":         func(in *CreatePoolInput) { in.BackendPort = 65536 },
		"glb id slash":      func(in *CreatePoolInput) { in.LoadBalancerID = "a/b" },
		"server id dot":     func(in *CreatePoolInput) { in.BackendServerID = ".." },
		"vpc id query":      func(in *CreatePoolInput) { in.BackendVPCID = "v?x" },
		"subnet id":         func(in *CreatePoolInput) { in.BackendSubnetID = "s s" },
		"missing project":   func(in *CreatePoolInput) { in.BackendProjectID = "" },
		"project mismatch":  func(in *CreatePoolInput) { in.BackendProjectID = "proj-2" },
		"project bad chars": func(in *CreatePoolInput) { in.BackendProjectID = "proj_1" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			in := poolInput()
			mutate(in)
			out, err := f.client(t).CreatePool(bg(), in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) || out != nil || len(f.calls) != 0 {
				t.Fatalf("out=%v err=%v calls=%v", out, err, f.calls)
			}
		})
	}
	if _, err := newFixture(t).client(t).CreatePool(bg(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err=%v", err)
	}
}

func TestCreatePoolRefusesWhenGuardsFail(t *testing.T) {
	srv := "GET " + vsPrefix + "/servers/" + fxServer
	vpc := "GET " + vsPrefix + "/networks/" + fxVPC
	sub := "GET " + vsPrefix + "/networks/" + fxVPC + "/subnets/" + fxSubnet
	iface := func(fixed, vpcID, subnetID, serverID string) string {
		return `{"serverUuid":"` + serverID + `","networkUuid":"` + vpcID + `","subnetUuid":"` + subnetID + `","fixedIp":"` + fixed + `"}`
	}
	server := func(status string, ifaces ...string) string {
		return `{"data":{"uuid":"srv-1","status":"` + status + `","internalInterfaces":[` + strings.Join(ifaces, ",") + `]}}`
	}
	good := iface("10.1.2.3", fxVPC, fxSubnet, fxServer)
	for name, set := range map[string]struct {
		key, body string
	}{
		"wrong server uuid":   {srv, `{"data":{"uuid":"srv-9","status":"ACTIVE","internalInterfaces":[` + good + `]}}`},
		"server not active":   {srv, server("SHUTOFF", good)},
		"no interface":        {srv, server("ACTIVE")},
		"two matching":        {srv, server("ACTIVE", good, iface("10.1.2.4", fxVPC, fxSubnet, fxServer))},
		"interface in other":  {srv, server("ACTIVE", iface("10.1.2.3", "vpc-9", fxSubnet, fxServer))},
		"other subnet":        {srv, server("ACTIVE", iface("10.1.2.3", fxVPC, "sub-9", fxServer))},
		"other server row":    {srv, server("ACTIVE", iface("10.1.2.3", fxVPC, fxSubnet, "srv-9"))},
		"public fixed ip":     {srv, server("ACTIVE", iface("203.0.113.9", fxVPC, fxSubnet, fxServer))},
		"outside subnet cidr": {srv, server("ACTIVE", iface("10.9.2.3", fxVPC, fxSubnet, fxServer))},
		"ipv6 fixed ip":       {srv, server("ACTIVE", iface("fd00::1", fxVPC, fxSubnet, fxServer))},
		"mapped fixed ip":     {srv, server("ACTIVE", iface("::ffff:10.1.2.3", fxVPC, fxSubnet, fxServer))},
		"noncanonical ip":     {srv, server("ACTIVE", iface("010.1.2.3", fxVPC, fxSubnet, fxServer))},
		"floating only":       {srv, `{"data":{"uuid":"srv-1","status":"ACTIVE","internalInterfaces":[{"serverUuid":"srv-1","networkUuid":"vpc-1","subnetUuid":"sub-1","floatingIp":"10.1.2.3"}]}}`},
		"vpc id differs":      {vpc, `{"id":"vpc-9","status":"ACTIVE","cidr":"10.1.0.0/16"}`},
		"vpc not active":      {vpc, `{"id":"vpc-1","status":"ERROR","cidr":"10.1.0.0/16"}`},
		"subnet id differs":   {sub, `{"uuid":"sub-9","networkUuid":"vpc-1","cidr":"10.1.2.0/24","status":"ACTIVE"}`},
		"subnet other vpc":    {sub, `{"uuid":"sub-1","networkUuid":"vpc-9","cidr":"10.1.2.0/24","status":"ACTIVE"}`},
		"subnet not active":   {sub, `{"uuid":"sub-1","networkUuid":"vpc-1","cidr":"10.1.2.0/24","status":"ERROR"}`},
		"duplicate key":       {srv, `{"data":{"uuid":"srv-1","uuid":"srv-1","status":"ACTIVE","internalInterfaces":[` + good + `]}}`},
		"case alias":          {srv, `{"data":{"uuid":"srv-1","UUID":"srv-1","status":"ACTIVE","internalInterfaces":[` + good + `]}}`},
		"malformed":           {srv, `{"data":`},
		"glb layer 7":         {"GET " + glbPrefix, `{"id":"glb-1","package":"pkg-1","type":"Layer 7","status":"ACTIVE"}`},
		"glb creating":        {"GET " + glbPrefix, `{"id":"glb-1","package":"pkg-1","type":"Layer 4","status":"CREATING"}`},
		"glb other id":        {"GET " + glbPrefix, `{"id":"glb-2","package":"pkg-1","type":"Layer 4","status":"ACTIVE"}`},
		"region inactive":     {"GET /v1/regions", `[{"id":"hcm","name":"HCM-3","status":"INACTIVE","vserverEndpoint":"https://hcm-3.api.vngcloud.vn/vserver/vserver-gateway"}]`},
		"region no match":     {"GET /v1/regions", `[{"id":"han","name":"HAN-1","status":"ACTIVE","vserverEndpoint":"https://han-1.api.vngcloud.vn/vserver/vserver-gateway"}]`},
		"endpoint differs":    {"GET /v1/regions", `[{"id":"hcm","name":"HCM-3","status":"","vserverEndpoint":"https://hcm-3.api.vngcloud.vn/vserver/vserver-gateway/"}]`},
		"endpoint missing":    {"GET /v1/regions", `[{"id":"hcm","name":"HCM-3","status":""}]`},
		"two regions":         {"GET /v1/regions", `[{"id":"hcm","name":"HCM-3","status":"ACTIVE","vserverEndpoint":"https://hcm-3.api.vngcloud.vn/vserver/vserver-gateway"},{"id":"hcm2","name":"HCM-3","status":"ACTIVE","vserverEndpoint":"https://hcm-3.api.vngcloud.vn/vserver/vserver-gateway"}]`},
		"package disabled":    {"GET /v1/packages", `[{"id":"pkg-1","enabled":false,"vlbPackages":[{"region":"hcm"}]}]`},
		"package no region":   {"GET /v1/packages", `[{"id":"pkg-1","enabled":true,"vlbPackages":[{"region":"han"}]}]`},
		"package missing":     {"GET /v1/packages", `[{"id":"pkg-2","enabled":true,"vlbPackages":[{"region":"hcm"}]}]`},
		"existing pool":       {"GET " + glbPrefix + "/global-pools", `[{"id":"other"}]`},
		"existing listener":   {"GET " + glbPrefix + "/global-listeners", `[{"id":"other"}]`},
		"null pools":          {"GET " + glbPrefix + "/global-pools", `null`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t).on(set.key, jsonBody(set.body))
			out, err := f.client(t).CreatePool(bg(), poolInput())
			if err == nil || out != nil || f.writes() != 0 || errors.Is(err, ErrNotSettled) {
				t.Fatalf("out=%v err=%v calls=%v", out, err, f.calls)
			}
		})
	}
}

func TestCreatePoolPreflightErrorKinds(t *testing.T) {
	f := newFixture(t).on("GET "+glbPrefix, status(404))
	_, err := f.client(t).CreatePool(bg(), poolInput())
	if !errors.Is(err, vngcloud.ErrNotFound) || f.writes() != 0 {
		t.Fatalf("404 err=%v", err)
	}
	f = newFixture(t).on("GET "+glbPrefix, status(403))
	_, err = f.client(t).CreatePool(bg(), poolInput())
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 403 || f.writes() != 0 {
		t.Fatalf("403 err=%v", err)
	}
	f = newFixture(t).on("GET "+glbPrefix, jsonBody(`{"id":`))
	_, err = f.client(t).CreatePool(bg(), poolInput())
	if !errors.As(err, &api) || api.Code != "InvalidResponse" || f.writes() != 0 {
		t.Fatalf("malformed err=%v", err)
	}
}

func TestCreatePoolDefinitiveRejection(t *testing.T) {
	for _, code := range []int{400, 401, 402, 403, 409, 422, 429} {
		f := newFixture(t).on("POST "+glbPrefix+"/global-pools", func(w httpResponseWriter, _ *httpRequest) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"bad tcp-backend 10.1.2.3 secret-value"}`))
		})
		_, err := f.client(t).CreatePool(bg(), poolInput())
		var api *vngcloud.APIError
		if !errors.As(err, &api) || api.StatusCode != code || errors.Is(err, ErrNotSettled) || f.count("POST ") != 1 {
			t.Fatalf("code=%d err=%v", code, err)
		}
		for _, leak := range []string{"tcp-backend", "10.1.2.3", "secret-value", "synthetic"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("code=%d error leaks %q: %v", code, leak, err)
			}
		}
	}
}

func TestCreatePoolAmbiguousOutcomesAreNotResent(t *testing.T) {
	for name, h := range map[string]httpHandlerFunc{
		"500":            status(500),
		"503":            status(503),
		"redirect":       func(w httpResponseWriter, _ *httpRequest) { w.Header().Set("Location", "/x"); w.WriteHeader(307) },
		"malformed":      jsonBody(`{"id":`),
		"empty":          status(202),
		"no id":          jsonBody(`{"globalLoadBalancerId":"glb-1"}`),
		"bad id":         jsonBody(`{"id":"a/b","globalLoadBalancerId":"glb-1"}`),
		"duplicate key":  jsonBody(`{"id":"gpool-1","id":"gpool-2","globalLoadBalancerId":"glb-1"}`),
		"case alias":     jsonBody(`{"id":"gpool-1","ID":"gpool-2","globalLoadBalancerId":"glb-1"}`),
		"wrong parent":   jsonBody(`{"id":"gpool-1","globalLoadBalancerId":"glb-9"}`),
		"credential":     jsonBody(`{"id":"gpool-1","globalLoadBalancerId":"glb-1","note":"x synthetic y"}`),
		"no group":       jsonBody(`{"id":"gpool-1","globalLoadBalancerId":"glb-1","globalPoolMembers":[]}`),
		"two groups":     jsonBody(`{"id":"gpool-1","globalLoadBalancerId":"glb-1","globalPoolMembers":[{"id":"a"},{"id":"b"}]}`),
		"bad group id":   jsonBody(`{"id":"gpool-1","globalLoadBalancerId":"glb-1","globalPoolMembers":[{"id":"a b"}]}`),
		"oversize":       jsonBody(strings.Repeat("x", 8*1024*1024+1)),
		"stray status":   status(204),
		"wrong 2xx body": jsonBody(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t).on("POST "+glbPrefix+"/global-pools", h)
			out, err := f.client(t).CreatePool(bg(), poolInput())
			if !errors.Is(err, ErrNotSettled) || f.count("POST ") != 1 {
				t.Fatalf("out=%v err=%v calls=%v", out, err, f.calls)
			}
			if strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("leak: %v", err)
			}
		})
	}
}

func TestCreatePoolPartialOutputCarriesSafeIDs(t *testing.T) {
	f := newFixture(t).on("GET "+glbPrefix+"/global-pools/"+fxPool, status(403))
	out, err := f.client(t).CreatePool(bg(), poolInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || out.PoolMember.ID != fxGroup || f.count("POST ") != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreatePoolWaitsThroughTransition(t *testing.T) {
	f := newFixture(t)
	reads := 0
	f.on("GET "+glbPrefix+"/global-pools/"+fxPool, func(w httpResponseWriter, _ *httpRequest) {
		reads++
		if reads < 3 {
			f.mu.Lock()
			f.poolStatus, f.groupStatus = "CREATING", "CREATING"
			f.mu.Unlock()
		} else {
			f.mu.Lock()
			f.poolStatus, f.groupStatus = "ACTIVE", "ACTIVE"
			f.mu.Unlock()
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		_, _ = w.Write([]byte(f.poolJSON()))
	})
	out, err := f.client(t).CreatePool(bg(), poolInput())
	if err != nil || out == nil || out.Pool.Status != "ACTIVE" || reads < 3 || f.count("POST ") != 1 {
		t.Fatalf("out=%+v err=%v reads=%d", out, err, reads)
	}
}

func TestCreatePoolFailureStates(t *testing.T) {
	f := newFixture(t)
	f.poolStatus = "ERROR"
	out, err := f.client(t).CreatePool(bg(), poolInput())
	if !errors.Is(err, ErrFailed) || errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || f.count("POST ") != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	f = newFixture(t)
	f.groupStatus = "SOMETHING-NEW"
	out, err = f.client(t).CreatePool(bg(), poolInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || strings.Contains(err.Error(), "SOMETHING-NEW") {
		t.Fatalf("unknown state out=%+v err=%v", out, err)
	}
}

func TestCreatePoolConfirmationMismatchesAreNotSettled(t *testing.T) {
	pool := "GET " + glbPrefix + "/global-pools/" + fxPool
	group := "GET " + glbPrefix + "/global-pools/" + fxPool + "/pool-members/" + fxGroup
	for name, set := range map[string]struct{ key, body string }{
		"pool name":        {pool, strings.Replace(newFixture(t).poolJSON(), `"tcp-pool"`, `"other"`, 1)},
		"pool protocol":    {pool, strings.Replace(newFixture(t).poolJSON(), `"protocol":"TCP","status"`, `"protocol":"HTTP","status"`, 1)},
		"pool algorithm":   {pool, strings.Replace(newFixture(t).poolJSON(), `ROUND_ROBIN`, `LEAST`, 1)},
		"pool parent":      {pool, strings.Replace(newFixture(t).poolJSON(), `"globalLoadBalancerId":"glb-1","algorithm"`, `"globalLoadBalancerId":"glb-9","algorithm"`, 1)},
		"pool id":          {pool, strings.Replace(newFixture(t).poolJSON(), `"id":"gpool-1","name"`, `"id":"gpool-9","name"`, 1)},
		"health interval":  {pool, strings.Replace(newFixture(t).poolJSON(), `"intervalTime":30`, `"intervalTime":31`, 1)},
		"health timeout":   {pool, strings.Replace(newFixture(t).poolJSON(), `"timeout":5`, `"timeout":6`, 1)},
		"health threshold": {pool, strings.Replace(newFixture(t).poolJSON(), `"healthyThreshold":3`, `"healthyThreshold":2`, 1)},
		"health pool id":   {pool, strings.Replace(newFixture(t).poolJSON(), `"globalPoolId":"gpool-1"`, `"globalPoolId":"gpool-9"`, 1)},
		"health missing":   {pool, `{"id":"gpool-1","name":"tcp-pool","globalLoadBalancerId":"glb-1","algorithm":"ROUND_ROBIN","protocol":"TCP","status":"ACTIVE","health":null}`},
		"group name":       {group, strings.Replace(newFixture(t).groupJSON(), `"name":"tcp-backend","region"`, `"name":"x","region"`, 1)},
		"group region":     {group, strings.Replace(newFixture(t).groupJSON(), `"hcm"`, `"han"`, 1)},
		"group vpc":        {group, strings.Replace(newFixture(t).groupJSON(), `"vpcId":"vpc-1"`, `"vpcId":"vpc-2"`, 1)},
		"group type":       {group, strings.Replace(newFixture(t).groupJSON(), `PRIVATE`, `PUBLIC`, 1)},
		"group dial":       {group, strings.Replace(newFixture(t).groupJSON(), `"trafficDial":100`, `"trafficDial":50`, 1)},
		"group pool link":  {group, strings.Replace(newFixture(t).groupJSON(), `"globalPoolId":"gpool-1"`, `"globalPoolId":"gpool-9"`, 1)},
		"row address":      {group, strings.Replace(newFixture(t).groupJSON(), `10.1.2.3`, `10.1.2.4`, 1)},
		"row subnet":       {group, strings.Replace(newFixture(t).groupJSON(), `"subnetId":"sub-1"`, `"subnetId":"sub-2"`, 1)},
		"row port":         {group, strings.Replace(newFixture(t).groupJSON(), `"port":8080`, `"port":9090`, 1)},
		"row monitor port": {group, strings.Replace(newFixture(t).groupJSON(), `"monitorPort":8080`, `"monitorPort":9090`, 1)},
		"row weight":       {group, strings.Replace(newFixture(t).groupJSON(), `"weight":1`, `"weight":2`, 1)},
		"row backup":       {group, strings.Replace(newFixture(t).groupJSON(), `"backupRole":false`, `"backupRole":true`, 1)},
		"row id":           {group, strings.Replace(newFixture(t).groupJSON(), `"id":"grow-1"`, `"id":"grow-9"`, 1)},
		"row parent":       {group, strings.Replace(newFixture(t).groupJSON(), `"globalPoolMemberId":"gmem-1"`, `"globalPoolMemberId":"gmem-9"`, 1)},
		"two rows":         {group, strings.Replace(newFixture(t).groupJSON(), `"members":[`, `"members":[{"id":"x"},`, 1)},
		"credential":       {group, strings.Replace(newFixture(t).groupJSON(), `"tcp-backend","region"`, `"a-synthetic-b","region"`, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t).on(set.key, jsonBody(set.body))
			out, err := f.client(t).CreatePool(bg(), poolInput())
			if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || f.count("POST ") != 1 || strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if name == "credential" && out.PoolMember.Name != "" {
				t.Fatalf("leaked value kept: %+v", out.PoolMember)
			}
		})
	}
}

func TestCreatePoolConfirmationNeedsExactlyOnePool(t *testing.T) {
	for name, body := range map[string]string{
		"extra pool":  `[` + newFixture(t).poolJSON() + `,{"id":"gpool-2"}]`,
		"other pool":  `[{"id":"gpool-2"}]`,
		"empty pools": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.on("GET "+glbPrefix+"/global-pools", func(w httpResponseWriter, _ *httpRequest) {
				if f.count("POST ") == 0 {
					_, _ = w.Write([]byte(`[]`))
					return
				}
				_, _ = w.Write([]byte(body))
			})
			out, err := f.client(t).CreatePool(bg(), poolInput())
			if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || f.count("POST ") != 1 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
	f := newFixture(t)
	f.on("GET "+glbPrefix+"/global-listeners", func(w httpResponseWriter, _ *httpRequest) {
		if f.count("POST ") == 0 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":"glis-1"}]`))
	})
	out, err := f.client(t).CreatePool(bg(), poolInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool {
		t.Fatalf("listener out=%+v err=%v", out, err)
	}
}

func TestCreatePoolWaitCancellationKeepsIDs(t *testing.T) {
	f := newFixture(t)
	f.poolStatus = "CREATING"
	ctx, cancel := context.WithTimeout(bg(), 50*time.Millisecond)
	defer cancel()
	out, err := f.client(t).CreatePool(ctx, poolInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Pool.ID != fxPool || out.PoolMember.ID != fxGroup || f.count("POST ") != 1 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreatePoolRefusesPublicAddressInsideSubnet(t *testing.T) {
	for _, ip := range []string{"203.0.113.9", "172.32.0.9", "100.64.0.9"} {
		cidr := ip[:strings.LastIndex(ip, ".")] + ".0/24"
		f := newFixture(t)
		f.on("GET "+vsPrefix+"/servers/"+fxServer, jsonBody(`{"data":{"uuid":"srv-1","status":"ACTIVE","internalInterfaces":[{"serverUuid":"srv-1","networkUuid":"vpc-1","subnetUuid":"sub-1","fixedIp":"`+ip+`"}]}}`))
		f.on("GET "+vsPrefix+"/networks/"+fxVPC+"/subnets/"+fxSubnet, jsonBody(`{"uuid":"sub-1","networkUuid":"vpc-1","cidr":"`+cidr+`","status":"ACTIVE"}`))
		out, err := f.client(t).CreatePool(bg(), poolInput())
		if err == nil || out != nil || f.writes() != 0 {
			t.Fatalf("ip=%s out=%v err=%v", ip, out, err)
		}
	}
}
