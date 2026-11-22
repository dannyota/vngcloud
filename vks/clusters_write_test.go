package vks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func validCreate() *CreateClusterInput {
	return &CreateClusterInput{Name: "cluster", Version: "version", VPCID: "vpc-1", SubnetID: "subnet-1", PodCIDR: "10.20.0.0/16", NodeGroupName: "group", NumNodes: 1, FlavorID: "flavor-1", OS: "ubuntu", DiskSize: 20, DiskTypeID: "disk-1", SecurityGroupIDs: []string{"security-1"}, SSHKeyID: "key-1"}
}

func TestCreateClusterWireAndWait(t *testing.T) {
	for _, code := range []int{200, 202} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			calls := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					if r.Method != "POST" || r.URL.Path != "/v1/clusters" || r.URL.RawQuery != "autoRenewal=false&poc=false" {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
					}
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Fatal(err)
					}
					var want map[string]any
					if err := json.Unmarshal([]byte(`{"name":"cluster","version":"version","releaseChannel":"STABLE","enablePrivateCluster":true,"networkType":"CILIUM_OVERLAY","vpcId":"vpc-1","cidr":"10.20.0.0/16","azStrategy":"SINGLE","listSubnetIds":["subnet-1"],"enabledLoadBalancerPlugin":false,"enabledBlockStoreCsiPlugin":false,"nodeGroup":{"name":"group","numNodes":1,"os":"ubuntu","flavorId":"flavor-1","diskSize":20,"diskType":"disk-1","enablePrivateNodes":true,"securityGroups":["security-1"],"sshKeyId":"key-1","subnetId":"subnet-1","upgradeConfig":{"strategy":"SURGE"}}}`), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("body = %v, want %v", got, want)
					}
					w.WriteHeader(code)
					_, _ = fmt.Fprint(w, `{"id":"cluster-1"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != "/v1/clusters/cluster-1" {
					t.Errorf("unexpected wait request %s %s", r.Method, r.URL.Path)
				}
				_, _ = fmt.Fprint(w, `{"id":"cluster-1","status":"ACTIVE"}`)
			})
			out, err := c.CreateCluster(context.Background(), validCreate())
			if err != nil || out.ID != "cluster-1" || out.Status != "ACTIVE" || calls != 2 {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestCreateClusterCanceledDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			w.WriteHeader(202)
			_, _ = fmt.Fprint(w, `{"id":"cluster-1"}`)
			return
		}
		cancel()
	})
	out, err := c.CreateCluster(ctx, validCreate())
	if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) || out.ID != "cluster-1" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreateClusterFailureAndTimeout(t *testing.T) {
	for _, status := range []string{"ERROR", "UPGRADE_FAILED", "CREATING"} {
		t.Run(status, func(t *testing.T) {
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					w.WriteHeader(202)
					_, _ = fmt.Fprint(w, `{"id":"cluster-1"}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":"cluster-1","status":%q}`, status)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			out, err := c.CreateCluster(ctx, validCreate())
			want := ErrFailed
			if status == "CREATING" {
				want = ErrNotSettled
			}
			if !errors.Is(err, want) || out.Status != status {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}

func TestCreateClusterMalformedReplyWithholdsInvalidFields(t *testing.T) {
	c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		_, _ = fmt.Fprint(w, `{"id":"bad/path","status":123}`)
	})
	out, err := c.CreateCluster(context.Background(), validCreate())
	if !errors.Is(err, ErrNotSettled) || out.ID != "" || out.Status != "" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreateClusterRejectsUnsafeStatus(t *testing.T) {
	c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		_, _ = fmt.Fprint(w, `{"id":"cluster-1","status":"secret value"}`)
	})
	in := validCreate()
	in.NoWait = true
	out, err := c.CreateCluster(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) || out.Status != "" {
		t.Fatalf("unsafe output=%+v err=%v", out, err)
	}
}

func TestCreateClusterDefiniteRejection(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		want          string
	}{
		{"zone", "Disk type does not match the subnet zone.", "Disk type does not match the subnet zone."},
		{"token", "Bearer test-token is invalid", "withheld"},
		{"id", "Subnet subnet-1 is invalid.", "redacted"},
		{"other-id", "Zone zone-abc123 does not match.", "redacted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": tc.message}})
			}, vngcloud.WithRetry(3, time.Millisecond))
			out, err := c.CreateCluster(context.Background(), validCreate())
			var api *vngcloud.APIError
			if !errors.As(err, &api) || api.StatusCode != 400 || errors.Is(err, ErrNotSettled) || !strings.Contains(api.Message, tc.want) || strings.Contains(api.Message, "test-token") || strings.Contains(api.Message, "subnet-1") || (out != nil && out.ID != "") || calls != 1 {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestCreateClusterUnauthorizedIsDefinite(t *testing.T) {
	calls := 0
	c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusUnauthorized) }, vngcloud.WithRetry(3, time.Millisecond))
	_, err := c.CreateCluster(context.Background(), validCreate())
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 401 || !errors.Is(err, vngcloud.ErrAuth) || errors.Is(err, ErrNotSettled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCreateClusterNoResend(t *testing.T) {
	for _, code := range []int{302, 408, 500, 202} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			calls := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if code == 302 {
					w.Header().Set("Location", "/redirect")
				}
				w.WriteHeader(code)
				_, _ = fmt.Fprint(w, `{}`)
			}, vngcloud.WithRetry(3, time.Millisecond))
			out, err := c.CreateCluster(context.Background(), validCreate())
			if !errors.Is(err, ErrNotSettled) || out.ID != "" || calls != 1 {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestDeleteClusterSingleSend(t *testing.T) {
	for _, code := range []int{200, 202, 401, 302, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			calls := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_, _ = fmt.Fprint(w, `{"id":"cluster-1","status":"ACTIVE"}`)
					return
				}
				if code == 302 {
					w.Header().Set("Location", "/redirect")
				}
				w.WriteHeader(code)
			}, vngcloud.WithRetry(3, time.Millisecond))
			out, err := c.DeleteCluster(context.Background(), &DeleteClusterInput{ClusterID: "cluster-1", NoWait: true})
			if calls != 2 || out == nil || out.Deleted {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
			if code == 200 || code == 202 {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrNotSettled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDeleteClusterPreflightAndWait(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(strconv.FormatBool(exists), func(t *testing.T) {
			calls := 0
			c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !exists || calls == 3 {
					http.NotFound(w, r)
					return
				}
				if calls == 1 {
					_, _ = fmt.Fprint(w, `{"id":"cluster-1","status":"ACTIVE"}`)
					return
				}
				if r.Method != "DELETE" {
					t.Errorf("method %s", r.Method)
				}
				w.WriteHeader(202)
			})
			out, err := c.DeleteCluster(context.Background(), &DeleteClusterInput{ClusterID: "cluster-1"})
			if !exists {
				if !errors.Is(err, vngcloud.ErrNotFound) || calls != 1 {
					t.Fatalf("err=%v calls=%d", err, calls)
				}
				return
			}
			if err != nil || !out.Deleted || calls != 3 {
				t.Fatalf("out=%+v err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func TestCreateClusterDoesNotHardcodeDiskBounds(t *testing.T) {
	calls := 0
	c := writeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(202)
		_, _ = fmt.Fprint(w, `{"id":"cluster-1"}`)
	})
	in := validCreate()
	in.DiskSize = 10
	in.NoWait = true
	if _, err := c.CreateCluster(context.Background(), in); err != nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCreateClusterRejectsIPv6CIDR(t *testing.T) {
	c := writeTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	in := validCreate()
	in.PodCIDR = "2001:db8::/64"
	if _, err := c.CreateCluster(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
}

func TestCreateClusterValidationNamesFieldAndRule(t *testing.T) {
	const secret = "private-value/secret"
	cases := []struct {
		field, rule string
		change      func(*CreateClusterInput)
	}{
		{"Name", "5 to 20", func(in *CreateClusterInput) { in.Name = secret }},
		{"NodeGroupName", "5 to 15", func(in *CreateClusterInput) { in.NodeGroupName = secret }},
		{"Version", "non-empty", func(in *CreateClusterInput) { in.Version = "" }},
		{"OS", "non-empty", func(in *CreateClusterInput) { in.OS = "" }},
		{"NumNodes", "1 to 10", func(in *CreateClusterInput) { in.NumNodes = -1 }},
		{"NumNodes", "1 to 10", func(in *CreateClusterInput) { in.NumNodes = 0 }},
		{"DiskSize", "positive", func(in *CreateClusterInput) { in.DiskSize = 0 }},
		{"SecurityGroupIDs", "at least one", func(in *CreateClusterInput) { in.SecurityGroupIDs = nil }},
		{"PodCIDR", "private IPv4", func(in *CreateClusterInput) { in.PodCIDR = secret }},
		{"VPCID", "letters, digits, and hyphens", func(in *CreateClusterInput) { in.VPCID = secret }},
		{"SubnetID", "letters, digits, and hyphens", func(in *CreateClusterInput) { in.SubnetID = secret }},
		{"FlavorID", "letters, digits, and hyphens", func(in *CreateClusterInput) { in.FlavorID = secret }},
		{"DiskTypeID", "letters, digits, and hyphens", func(in *CreateClusterInput) { in.DiskTypeID = secret }},
		{"SSHKeyID", "letters, digits, and hyphens", func(in *CreateClusterInput) { in.SSHKeyID = secret }},
		{"SecurityGroupIDs", "letters, digits, and hyphens", func(in *CreateClusterInput) { in.SecurityGroupIDs = []string{secret} }},
	}
	c := writeTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	for _, tc := range cases {
		t.Run(tc.field+"/"+tc.rule, func(t *testing.T) {
			in := validCreate()
			tc.change(in)
			_, err := c.CreateCluster(context.Background(), in)
			if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), tc.rule) || strings.Contains(err.Error(), secret) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDeleteClusterInvalidIDWithholdsValue(t *testing.T) {
	c := writeTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	_, err := c.DeleteCluster(context.Background(), &DeleteClusterInput{ClusterID: "private-value/secret"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), "ClusterID") || !strings.Contains(err.Error(), "letters, digits, and hyphens") || strings.Contains(err.Error(), "private-value/secret") {
		t.Fatalf("error=%v", err)
	}
}

func TestCreateClusterInvalidInputDoesNotSend(t *testing.T) {
	c := writeTestClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") })
	for name, change := range map[string]func(*CreateClusterInput){
		"name":         func(in *CreateClusterInput) { in.Name = "bad" },
		"group":        func(in *CreateClusterInput) { in.NodeGroupName = "bad" },
		"vpc":          func(in *CreateClusterInput) { in.VPCID = "../unsafe" },
		"subnet":       func(in *CreateClusterInput) { in.SubnetID = "" },
		"cidr":         func(in *CreateClusterInput) { in.PodCIDR = "not-cidr" },
		"public-cidr":  func(in *CreateClusterInput) { in.PodCIDR = "8.8.0.0/16" },
		"count-zero":   func(in *CreateClusterInput) { in.NumNodes = 0 },
		"count-eleven": func(in *CreateClusterInput) { in.NumNodes = 11 },
		"flavor":       func(in *CreateClusterInput) { in.FlavorID = "" },
		"disk":         func(in *CreateClusterInput) { in.DiskTypeID = "" },
		"groups":       func(in *CreateClusterInput) { in.SecurityGroupIDs = nil },
		"key":          func(in *CreateClusterInput) { in.SSHKeyID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			in := validCreate()
			change(in)
			if _, err := c.CreateCluster(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if _, err := c.CreateCluster(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("error = %v, want invalid input", err)
	}
}
