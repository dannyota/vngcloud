package globalloadbalancer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

const (
	fxGLB      = "glb-1"
	fxProject  = "proj-1"
	fxServer   = "srv-1"
	fxVPC      = "vpc-1"
	fxSubnet   = "sub-1"
	fxPool     = "gpool-1"
	fxGroup    = "gmem-1"
	fxRow      = "grow-1"
	fxListener = "glis-1"
	fxCIDRs    = "192.0.2.5/32,198.51.100.0/24"
	vsPrefix   = "/vserver/iam-vserver-gateway/v2/" + fxProject
	glbPrefix  = "/v1/global-load-balancers/" + fxGLB
)

type fixture struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []string
	bodies   map[string][]byte
	override map[string]http.HandlerFunc

	pool, group, listener                       bool
	poolStatus, groupStatus, listenerStatus     string
	acceptStatus                                int
	poolName, groupName, rowAddr, listenerCIDRs string
	listenerPool                                string
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, bodies: map[string][]byte{}, override: map[string]http.HandlerFunc{}, poolStatus: "ACTIVE", groupStatus: "OFFLINE", listenerStatus: "ACTIVE", acceptStatus: 202, poolName: "tcp-pool", groupName: "tcp-backend", rowAddr: "10.1.2.3", listenerCIDRs: fxCIDRs, listenerPool: fxPool}
}

func (f *fixture) withChildren() *fixture {
	f.pool, f.group, f.listener = true, true, true
	return f
}

func (f *fixture) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fixture) writes() int {
	return f.count("POST ") + f.count("DELETE ")
}

func (f *fixture) body(key string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func (f *fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + r.URL.Path
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.bodies[key] = raw
	h := f.override[key]
	f.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	f.defaults(w, key)
}

func (f *fixture) client(t *testing.T) *Client {
	c := newWriteTestClientOpts(t, f, vngcloud.WithProjectID(fxProject))
	c.poll = time.Millisecond
	return c
}

func (f *fixture) healthJSON() string {
	return fmt.Sprintf(`{"id":"ghc-1","globalPoolId":%q,"globalLoadBalancerId":%q,"protocol":"TCP","timeout":5,"intervalTime":30,"healthyThreshold":3,"unhealthyThreshold":3,"status":%q}`, fxPool, fxGLB, f.poolStatus)
}

func (f *fixture) poolJSON() string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"globalLoadBalancerId":%q,"algorithm":"ROUND_ROBIN","protocol":"TCP","status":%q,"health":%s}`, fxPool, f.poolName, fxGLB, f.poolStatus, f.healthJSON())
}

func (f *fixture) groupJSON() string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"region":"hcm","globalPoolId":%q,"globalLoadBalancerId":%q,"trafficDial":100,"vpcId":%q,"type":"PRIVATE","status":%q,"members":[{"id":%q,"name":"tcp-backend","globalPoolMemberId":%q,"globalLoadBalancerId":%q,"subnetId":%q,"address":%q,"weight":1,"port":8080,"monitorPort":8080,"backupRole":false,"status":%q}]}`, fxGroup, f.groupName, fxPool, fxGLB, fxVPC, f.groupStatus, fxRow, fxGroup, fxGLB, fxSubnet, f.rowAddr, f.groupStatus)
}

func (f *fixture) listenerJSON() string {
	return fmt.Sprintf(`{"id":%q,"name":"tcp-listener","protocol":"TCP","port":443,"globalLoadBalancerId":%q,"globalPoolId":%q,"timeoutClient":50,"timeoutConnection":5,"timeoutMember":50,"allowedCidrs":%q,"status":%q}`, fxListener, fxGLB, f.listenerPool, f.listenerCIDRs, f.listenerStatus)
}

func list(on bool, item string) string {
	if on {
		return "[" + item + "]"
	}
	return "[]"
}

func (f *fixture) defaults(w http.ResponseWriter, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(body string) { _, _ = w.Write([]byte(body)) }
	missing := func() { w.WriteHeader(http.StatusNotFound) }
	switch key {
	case "GET " + glbPrefix:
		reply(`{"id":"glb-1","name":"g","package":"pkg-1","type":"Layer 4","status":"ACTIVE"}`)
	case "GET /v1/regions":
		reply(`[{"id":"hcm","name":"HCM-3","status":"","vserverEndpoint":"https://hcm-3.api.vngcloud.vn/vserver/vserver-gateway"},{"id":"han","name":"HAN-1","status":"","vserverEndpoint":"https://han-1.api.vngcloud.vn/vserver/vserver-gateway"}]`)
	case "GET /v1/packages":
		reply(`[{"id":"pkg-1","enabled":true,"vlbPackages":[{"region":"hcm"},{"region":"han"}]}]`)
	case "GET " + vsPrefix + "/servers/" + fxServer:
		reply(`{"data":{"uuid":"srv-1","status":"ACTIVE","internalInterfaces":[{"serverUuid":"srv-1","networkUuid":"vpc-1","subnetUuid":"sub-1","fixedIp":"10.1.2.3","floatingIp":"203.0.113.9"}]}}`)
	case "GET " + vsPrefix + "/networks/" + fxVPC:
		reply(`{"id":"vpc-1","status":"ACTIVE","cidr":"10.1.0.0/16"}`)
	case "GET " + vsPrefix + "/networks/" + fxVPC + "/subnets/" + fxSubnet:
		reply(`{"uuid":"sub-1","networkUuid":"vpc-1","cidr":"10.1.2.0/24","status":"ACTIVE"}`)
	case "GET " + glbPrefix + "/global-pools":
		reply(list(f.pool, f.poolJSON()))
	case "GET " + glbPrefix + "/global-pools/" + fxPool:
		if !f.pool {
			missing()
			return
		}
		reply(f.poolJSON())
	case "GET " + glbPrefix + "/global-pools/" + fxPool + "/pool-members":
		reply(list(f.pool && f.group, f.groupJSON()))
	case "GET " + glbPrefix + "/global-pools/" + fxPool + "/pool-members/" + fxGroup:
		if !f.pool || !f.group {
			missing()
			return
		}
		reply(f.groupJSON())
	case "GET " + glbPrefix + "/global-listeners":
		reply(list(f.listener, f.listenerJSON()))
	case "GET " + glbPrefix + "/global-listeners/" + fxListener:
		if !f.listener {
			missing()
			return
		}
		reply(f.listenerJSON())
	case "POST " + glbPrefix + "/global-pools":
		f.pool, f.group = true, true
		w.WriteHeader(f.acceptStatus)
		reply(fmt.Sprintf(`{"id":%q,"globalLoadBalancerId":%q,"health":%s,"globalPoolMembers":[{"id":%q,"members":[{"id":%q}]}]}`, fxPool, fxGLB, f.healthJSON(), fxGroup, fxRow))
	case "POST " + glbPrefix + "/global-listeners":
		f.listener = true
		w.WriteHeader(f.acceptStatus)
		reply(fmt.Sprintf(`{"id":%q,"globalLoadBalancerId":%q,"globalPoolId":%q}`, fxListener, fxGLB, fxPool))
	case "DELETE " + glbPrefix + "/global-listeners/" + fxListener:
		f.listener = false
		w.WriteHeader(f.acceptStatus)
	case "DELETE " + glbPrefix + "/global-pools/" + fxPool:
		f.pool, f.group = false, false
		w.WriteHeader(f.acceptStatus)
	default:
		f.t.Errorf("unexpected request %s", key)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (f *fixture) on(key string, h http.HandlerFunc) *fixture {
	f.override[key] = h
	return f
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func jsonBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func poolInput() *CreatePoolInput {
	return &CreatePoolInput{LoadBalancerID: fxGLB, Name: "tcp-pool", MemberName: "tcp-backend", BackendProjectID: fxProject, BackendServerID: fxServer, BackendVPCID: fxVPC, BackendSubnetID: fxSubnet, BackendPort: 8080}
}

func listenerInput() *CreateListenerInput {
	return &CreateListenerInput{LoadBalancerID: fxGLB, Name: "tcp-listener", PoolID: fxPool, Port: 443, AllowedCIDRs: []string{"192.0.2.5/32", "198.51.100.0/24"}, BackendProjectID: fxProject, BackendServerID: fxServer}
}

func bg() context.Context { return context.Background() }

type (
	httpResponseWriter = http.ResponseWriter
	httpRequest        = http.Request
	httpHandlerFunc    = http.HandlerFunc
)
