package loadbalancer

import (
	"context"
	"testing"
)

func TestMemberNoWaitReturnsSubmittedMembers(t *testing.T) {
	c := newTestClient(t, memberTestHandler(t, nil, ""))
	out, err := c.AddPoolMember(context.Background(), &AddPoolMemberInput{
		LoadBalancerID: "lb-1", PoolID: "pool-1", Address: "10.0.0.3", Port: 8080, NoWait: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Pool.UUID != "pool-1" || len(out.Pool.Members) != 3 {
		t.Fatalf("pool = %+v, want three submitted members", out.Pool)
	}
	member := out.Pool.Members[2]
	if member.Address != "10.0.0.3" || member.ProtocolPort != 8080 || member.MonitorPort != 8080 || member.Weight != 1 {
		t.Fatalf("new member = %+v", member)
	}
	if out.Pool.Members[0].UUID != "member-1" || out.Pool.Members[1].UUID != "member-2" {
		t.Fatalf("existing member identities lost: %+v", out.Pool.Members)
	}
}
