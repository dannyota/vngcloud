package globalloadbalancer

import (
	"context"
	"net/netip"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

type placement struct {
	Address  string
	RegionID string
}

func validID(op, field, value string) error {
	return core.CheckPathID(op, field, value)
}

func (r *run) parent(ctx context.Context, glb string) (wireGLB, error) {
	var got wireGLB
	if err := r.get(ctx, r.c.glbChildURL(glb), &got); err != nil {
		return got, err
	}
	if got.ID != glb || got.Type != "Layer 4" || !parentEligible(got.Status) || got.Package == "" {
		return got, refuse("the load balancer is not an eligible Layer 4 load balancer")
	}
	return got, nil
}

func (r *run) parentWhileCreating(ctx context.Context, glb string) (stateClass, error) {
	var got wireGLB
	if err := r.get(ctx, r.c.glbChildURL(glb), &got); err != nil {
		return stateUnknown, err
	}
	class := createState(got.Status)
	if got.ID != glb || got.Type != "Layer 4" || (class != stateSuccess && class != stateTransitional) {
		return stateUnknown, refuse("the load balancer is no longer the expected Layer 4 load balancer")
	}
	return class, nil
}

func (r *run) project(ctx context.Context, requested string) (string, error) {
	if err := validID(r.op, "BackendProjectID", requested); err != nil {
		return "", err
	}
	project, err := r.c.c.RequireProjectID(r.guard(ctx))
	if err != nil {
		return "", err
	}
	if project != requested {
		return "", refuse("BackendProjectID must equal the client's project")
	}
	return project, nil
}

func (r *run) region(ctx context.Context, packageID string) (string, error) {
	var regions []wireRegion
	if err := r.get(ctx, r.c.glbURL([]string{"regions"}, nil), &regions); err != nil {
		return "", err
	}
	var packages []wirePackage
	if err := r.get(ctx, r.c.glbURL([]string{"packages"}, nil), &packages); err != nil {
		return "", err
	}
	configured := r.c.c.Region()
	var match []wireRegion
	for _, region := range regions {
		if regionMatches(configured, region) {
			match = append(match, region)
		}
	}
	if len(match) != 1 || match[0].ID == "" {
		return "", refuse("the configured region does not match exactly one enabled GLB region")
	}
	var found []wirePackage
	for _, p := range packages {
		if p.ID == packageID {
			found = append(found, p)
		}
	}
	if len(found) != 1 || !packageSupports(found[0], configured, match[0].ID) {
		return "", refuse("the load balancer package is not enabled for the configured region")
	}
	return match[0].ID, nil
}

func (r *run) placement(ctx context.Context, project, serverID, vpcID, subnetID string) (placement, error) {
	var none placement
	for field, value := range map[string]string{"BackendServerID": serverID, "BackendVPCID": vpcID, "BackendSubnetID": subnetID} {
		if err := validID(r.op, field, value); err != nil {
			return none, err
		}
	}
	var server wireServer
	if err := r.get(ctx, r.c.vserverURL(project, "servers", serverID), &server); err != nil {
		return none, err
	}
	var vpc wireVPC
	if err := r.get(ctx, r.c.vserverURL(project, "networks", vpcID), &vpc); err != nil {
		return none, err
	}
	var subnet wireSubnet
	if err := r.get(ctx, r.c.vserverURL(project, "networks", vpcID, "subnets", subnetID), &subnet); err != nil {
		return none, err
	}
	if server.Data.UUID != serverID || !strings.EqualFold(server.Data.Status, "ACTIVE") || vpc.ID != vpcID || !strings.EqualFold(vpc.Status, "ACTIVE") ||
		subnet.UUID != subnetID || subnet.NetworkUUID != vpcID || !strings.EqualFold(subnet.Status, "ACTIVE") {
		return none, refuse("the backend server, VPC and subnet are not an exact eligible match")
	}
	var matches []wireInterface
	for _, nic := range server.Data.InternalInterfaces {
		if nic.ServerUUID == serverID && nic.NetworkUUID == vpcID && nic.SubnetUUID == subnetID {
			matches = append(matches, nic)
		}
	}
	if len(matches) != 1 {
		return none, refuse("the backend server needs exactly one internal interface in the requested VPC and subnet")
	}
	addr, err := netip.ParseAddr(matches[0].FixedIP)
	cidr, cerr := netip.ParsePrefix(subnet.CIDR)
	if err != nil || cerr != nil || !addr.Is4() || addr.String() != matches[0].FixedIP || !addr.IsPrivate() || !cidr.Contains(addr) {
		return none, refuse("the backend address is not a private IPv4 address inside the subnet")
	}
	return placement{Address: addr.String()}, nil
}

type chain struct {
	pool    wirePool
	group   wireGroup
	verdict stateClass
}

type chainSpec struct {
	glb      string
	poolID   string
	groupID  string
	poolName string
	backend  *backendWant
}

type backendWant struct {
	Name, Region, VPC, Subnet, Address string
	Port                               int
}

func combine(classes ...stateClass) stateClass {
	out := stateSuccess
	for _, c := range classes {
		switch {
		case c == stateUnknown:
			return stateUnknown
		case c == stateFailed:
			out = stateFailed
		case c == stateTransitional && out == stateSuccess:
			out = stateTransitional
		}
	}
	return out
}

func checkPool(p wirePool, glb, id, name string) bool {
	h := p.Health
	return p.ID == id && p.GlobalLoadBalancerID == glb && (name == "" || p.Name == name) && p.Protocol == wireProtocol && p.Algorithm == wireAlgorithm &&
		h != nil && h.Protocol == wireProtocol && h.HealthyThreshold == wireHealthy && h.UnhealthyThreshold == wireUnhealthy && h.IntervalTime == wireInterval && h.Timeout == wireHealthTime &&
		h.GlobalPoolID == id && h.GlobalLoadBalancerID == glb
}

func checkGroup(g wireGroup, glb, poolID, id string, want *backendWant) bool {
	if g.ID != id || g.ID == "" || g.GlobalPoolID != poolID || g.GlobalLoadBalancerID != glb || g.Type != wireGroupType || g.TrafficDial != wireTrafficDial || len(g.Members) != 1 {
		return false
	}
	row := g.Members[0]
	if row.ID == "" || row.GlobalPoolMemberID != id || row.GlobalLoadBalancerID != glb || row.BackupRole == nil || *row.BackupRole || row.Weight != wireWeight || row.Port < 1 || row.Port > 65535 || row.MonitorPort != row.Port || row.SubnetID == "" || row.Address == "" {
		return false
	}
	return want == nil || (g.Name == want.Name && row.Name == want.Name && g.Region == want.Region && g.VPCID == want.VPC && row.SubnetID == want.Subnet && row.Address == want.Address && row.Port == want.Port)
}

func (r *run) readChain(ctx context.Context, spec chainSpec) (chain, error) {
	var out chain
	pools, err := r.pools(ctx, spec.glb)
	if err != nil {
		return out, err
	}
	if len(pools) != 1 || pools[0].ID != spec.poolID {
		return out, refuse("the load balancer must hold exactly the expected pool")
	}
	if out.pool, err = r.pool(ctx, spec.glb, spec.poolID); err != nil {
		return out, err
	}
	if !checkPool(out.pool, spec.glb, spec.poolID, spec.poolName) {
		return out, refuse("the pool does not match the supported TCP configuration")
	}
	groups, err := r.groups(ctx, spec.glb, spec.poolID)
	if err != nil {
		return out, err
	}
	if len(groups) != 1 || (spec.groupID != "" && groups[0].ID != spec.groupID) {
		return out, refuse("the pool must hold exactly the expected member group")
	}
	if err := validID(r.op, "PoolMemberID", groups[0].ID); err != nil {
		return out, r.invalidResponse()
	}
	if out.group, err = r.group(ctx, spec.glb, spec.poolID, groups[0].ID); err != nil {
		return out, err
	}
	if !checkGroup(out.group, spec.glb, spec.poolID, groups[0].ID, spec.backend) {
		return out, refuse("the member group does not match the supported private backend")
	}
	out.verdict = combine(createState(out.pool.Status), createState(out.pool.Health.Status), memberState(out.group.Status), memberState(out.group.Members[0].Status))
	return out, nil
}
