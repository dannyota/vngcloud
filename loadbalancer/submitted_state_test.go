package loadbalancer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func TestPolicyNoWaitReturnsSubmittedFields(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(strconv.FormatBool(create), func(t *testing.T) {
			writes := 0
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					_, _ = fmt.Fprint(w, `{"uuid":"policy-1"}`)
					return
				}
				if r.URL.Path == policyLBPath {
					policyLBHandler(w, r)
					return
				}
				_, _ = fmt.Fprint(w, `{"data":{"uuid":"policy-1","name":"policy","action":"REDIRECT_TO_URL","redirectUrl":"https://old.example.test","redirectHttpCode":302,"keepQueryString":false,"progressStatus":"CREATED","l7Rules":[{"uuid":"old-rule","ruleType":"PATH","compareType":"EQUAL_TO","ruleValue":"/old"}]}}`)
			}))
			rules := []PolicyRuleInput{{Type: "PATH", CompareType: "STARTS_WITH", Value: "/new"}}
			var policy Policy
			if create {
				out, err := c.CreatePolicy(context.Background(), &CreatePolicyInput{
					LoadBalancerID: "lb-1", ListenerID: "listener-1", Name: "policy", Action: ActionRedirectToURL,
					RedirectURL: "https://new.example.test", RedirectHTTPCode: 301, KeepQueryString: true, Rules: rules, NoWait: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				policy = out.Policy
			} else {
				out, err := c.UpdatePolicy(context.Background(), &UpdatePolicyInput{
					LoadBalancerID: "lb-1", ListenerID: "listener-1", PolicyID: "policy-1",
					RedirectURL: vngcloud.Ptr("https://new.example.test"), RedirectHTTPCode: vngcloud.Ptr(301),
					KeepQueryString: vngcloud.Ptr(true), Rules: &rules, NoWait: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				policy = out.Policy
			}
			if writes != 1 || policy.UUID != "policy-1" || policy.Action != ActionRedirectToURL ||
				policy.RedirectURL != "https://new.example.test" || policy.RedirectHTTPCode != 301 || !policy.KeepQueryString ||
				!reflect.DeepEqual(policy.L7Rules, []L7Rule{{RuleType: "PATH", CompareType: "STARTS_WITH", RuleValue: "/new"}}) {
				t.Fatalf("writes = %d, policy = %+v", writes, policy)
			}
		})
	}
}

func TestPoolNoWaitReturnsSubmittedFields(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(strconv.FormatBool(create), func(t *testing.T) {
			var sent createPoolBody
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method != http.MethodGet:
					if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
						t.Error(err)
					}
					_, _ = fmt.Fprint(w, `{"uuid":"pool-1"}`)
				case r.URL.Path == poolLBPath:
					poolLBHandler(w, r)
				case strings.HasSuffix(r.URL.Path, "/healthMonitor"):
					_, _ = fmt.Fprint(w, `{"data":{"healthCheckProtocol":"HTTP","timeout":5,"interval":30,"healthyThreshold":3,"unhealthyThreshold":3,"healthCheckPath":"/old","healthCheckMethod":"GET","httpVersion":"1.0","successCode":"200","domainName":"old.example.test"}}`)
				default:
					_, _ = fmt.Fprint(w, `{"data":{"uuid":"pool-1","protocol":"HTTP","loadBalanceMethod":"ROUND_ROBIN","stickiness":true,"tlsEncryption":false,"progressStatus":"CREATED"}}`)
				}
			}))
			var pool Pool
			if create {
				in := validCreatePoolInput()
				in.Stickiness, in.TLSEncryption = vngcloud.Ptr(false), vngcloud.Ptr(true)
				in.Algorithm, in.HealthCheckPath, in.HealthCheckMethod = AlgorithmSourceIP, "/health", "POST"
				in.HealthCheckDomainName, in.HealthCheckHTTPVersion, in.HealthCheckSuccessCode = "origin.example.test", "1.1", "204"
				in.HealthyThreshold, in.UnhealthyThreshold, in.HealthCheckInterval, in.HealthCheckTimeout = 4, 5, 20, 7
				in.NoWait = true
				out, err := c.CreatePool(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				pool = out.Pool
			} else {
				out, err := c.UpdatePool(context.Background(), &UpdatePoolInput{
					LoadBalancerID: "lb-1", PoolID: "pool-1", Algorithm: vngcloud.Ptr(AlgorithmSourceIP),
					Stickiness: vngcloud.Ptr(false), TLSEncryption: vngcloud.Ptr(true),
					HealthCheckPath: vngcloud.Ptr("/health"), HealthCheckMethod: vngcloud.Ptr("POST"),
					HealthCheckDomainName: vngcloud.Ptr("origin.example.test"), HealthCheckHTTPVersion: vngcloud.Ptr("1.1"),
					HealthCheckSuccessCode: vngcloud.Ptr("204"), HealthyThreshold: vngcloud.Ptr(4), UnhealthyThreshold: vngcloud.Ptr(5),
					HealthCheckInterval: vngcloud.Ptr(20), HealthCheckTimeout: vngcloud.Ptr(7), NoWait: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				pool = out.Pool
			}
			hm := pool.HealthMonitor
			if pool.UUID != "pool-1" || pool.LoadBalanceMethod != AlgorithmSourceIP || pool.Stickiness || !pool.TLSEncryption || hm == nil {
				t.Fatalf("pool = %+v", pool)
			}
			if hm.HealthCheckProtocol != "HTTP" || hm.Timeout != 7 || hm.Interval != 20 || hm.HealthyThreshold != 4 || hm.UnhealthyThreshold != 5 ||
				hm.HealthCheckPath == nil || *hm.HealthCheckPath != "/health" || hm.HealthCheckMethod == nil || *hm.HealthCheckMethod != "POST" ||
				hm.DomainName == nil || *hm.DomainName != "origin.example.test" || hm.HTTPVersion == nil || *hm.HTTPVersion != "1.1" ||
				hm.SuccessCode == nil || *hm.SuccessCode != "204" || sent.HealthMonitor.Timeout != hm.Timeout {
				t.Fatalf("health monitor = %+v, sent = %+v", hm, sent)
			}
		})
	}
}

func TestListenerNoWaitReturnsSubmittedFields(t *testing.T) {
	for _, create := range []bool{true, false} {
		t.Run(strconv.FormatBool(create), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					_, _ = fmt.Fprint(w, `{"uuid":"listener-1"}`)
					return
				}
				if r.URL.Path == listenerLBPath {
					listenerLBHandler(w, r)
					return
				}
				_, _ = fmt.Fprint(w, `{"data":{"uuid":"listener-1","protocol":"HTTPS","protocolPort":443,"progressStatus":"CREATED","timeoutClient":50,"timeoutMember":50,"timeoutConnection":5,"allowedCidrs":"10.0.0.0/24","defaultCertificateAuthority":"cert-old","certificateAuthorities":["cert-old"],"clientCertificateAuthentication":"client-old","insertHeaders":[{"headerName":"X-Old","headerValue":"old"}]}}`)
			}))
			headers := []ListenerInsertHeader{{HeaderName: "X-Test", HeaderValue: "value"}}
			certificates := []string{"cert-1", "cert-2"}
			var listener Listener
			if create {
				in := validCreateListenerInput()
				in.Protocol, in.Port, in.DefaultPoolID = ProtocolHTTPS, 443, "pool-1"
				in.TimeoutClient, in.TimeoutMember, in.TimeoutConnection = 10, 20, 3
				in.CertificateIDs, in.DefaultCertificateID, in.ClientCertificateID = certificates, "cert-1", "client-1"
				in.InsertHeaders, in.NoWait = headers, true
				out, err := c.CreateListener(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				listener = out.Listener
			} else {
				out, err := c.UpdateListener(context.Background(), &UpdateListenerInput{
					LoadBalancerID: "lb-1", ListenerID: "listener-1", DefaultPoolID: vngcloud.Ptr("pool-1"),
					TimeoutClient: vngcloud.Ptr(10), TimeoutMember: vngcloud.Ptr(20), TimeoutConnection: vngcloud.Ptr(3),
					CertificateIDs: &certificates, DefaultCertificateID: vngcloud.Ptr("cert-1"), ClientCertificateID: vngcloud.Ptr("client-1"),
					InsertHeaders: &headers, NoWait: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				listener = out.Listener
			}
			if listener.UUID != "listener-1" || listener.DefaultPoolID != "pool-1" || listener.AllowedCIDRs != "10.0.0.0/24" ||
				listener.TimeoutClient != 10 || listener.TimeoutMember != 20 || listener.TimeoutConnection != 3 ||
				!reflect.DeepEqual(listener.CertificateAuthorities, certificates) || !reflect.DeepEqual(listener.InsertHeaders, headers) ||
				listener.DefaultCertificateAuthority == nil || *listener.DefaultCertificateAuthority != "cert-1" ||
				listener.ClientCertificateAuthentication == nil || *listener.ClientCertificateAuthentication != "client-1" {
				t.Fatalf("listener = %+v", listener)
			}
		})
	}
}
