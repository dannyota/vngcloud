package globalloadbalancer

type wireGLB struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	Status  string `json:"status"`
	Type    string `json:"type"`
}

type wireRegion struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Status *string `json:"status"`

	VServerEndpoint string `json:"vserverEndpoint"`
}

type wirePackage struct {
	ID          string `json:"id"`
	Enabled     *bool  `json:"enabled"`
	VLBPackages []struct {
		Region       string `json:"region"`
		GLBPackageID string `json:"glb_package_id"`
		VLBPackageID string `json:"vlb_package_id"`
	} `json:"vlbPackages"`
}

type wireInterface struct {
	ServerUUID  string `json:"serverUuid"`
	NetworkUUID string `json:"networkUuid"`
	SubnetUUID  string `json:"subnetUuid"`
	FixedIP     string `json:"fixedIp"`
}

type wireServer struct {
	Data struct {
		UUID               string          `json:"uuid"`
		Status             string          `json:"status"`
		InternalInterfaces []wireInterface `json:"internalInterfaces"`
	} `json:"data"`
}

type wireVPC struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	CIDR   string `json:"cidr"`
}

type wireSubnet struct {
	UUID        string `json:"uuid"`
	NetworkUUID string `json:"networkUuid"`
	CIDR        string `json:"cidr"`
	Status      string `json:"status"`
}

type wireHealth struct {
	ID                   string `json:"id"`
	GlobalPoolID         string `json:"globalPoolId"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	Protocol             string `json:"protocol"`
	Timeout              int    `json:"timeout"`
	IntervalTime         int    `json:"intervalTime"`
	HealthyThreshold     int    `json:"healthyThreshold"`
	UnhealthyThreshold   int    `json:"unhealthyThreshold"`
	Status               string `json:"status"`
}

type wirePool struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	GlobalLoadBalancerID string      `json:"globalLoadBalancerId"`
	Algorithm            string      `json:"algorithm"`
	Protocol             string      `json:"protocol"`
	Status               string      `json:"status"`
	Health               *wireHealth `json:"health"`
}

type wireRow struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	GlobalPoolMemberID   string `json:"globalPoolMemberId"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	SubnetID             string `json:"subnetId"`
	Address              string `json:"address"`
	Weight               int    `json:"weight"`
	Port                 int    `json:"port"`
	MonitorPort          int    `json:"monitorPort"`
	BackupRole           *bool  `json:"backupRole"`
	Status               string `json:"status"`
}

type wireGroup struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Region               string    `json:"region"`
	GlobalPoolID         string    `json:"globalPoolId"`
	GlobalLoadBalancerID string    `json:"globalLoadBalancerId"`
	TrafficDial          int       `json:"trafficDial"`
	VPCID                string    `json:"vpcId"`
	Type                 string    `json:"type"`
	Status               string    `json:"status"`
	Members              []wireRow `json:"members"`
}

type wireListener struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Protocol             string `json:"protocol"`
	Port                 int    `json:"port"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	GlobalPoolID         string `json:"globalPoolId"`
	TimeoutClient        int    `json:"timeoutClient"`
	TimeoutConnection    int    `json:"timeoutConnection"`
	TimeoutMember        int    `json:"timeoutMember"`
	AllowedCIDRs         string `json:"allowedCidrs"`
	Status               string `json:"status"`
}

type wireAccept struct {
	ID                   string `json:"id"`
	GlobalLoadBalancerID string `json:"globalLoadBalancerId"`
	GlobalPoolID         string `json:"globalPoolId"`
	Groups               []struct {
		ID      string `json:"id"`
		Members []struct {
			ID string `json:"id"`
		} `json:"members"`
	} `json:"globalPoolMembers"`
}

type poolBody struct {
	Name      string      `json:"name"`
	Protocol  string      `json:"protocol"`
	Algorithm string      `json:"algorithm"`
	Health    healthBody  `json:"health"`
	Groups    []groupBody `json:"globalPoolMembers"`
}

type healthBody struct {
	Protocol           string `json:"protocol"`
	HealthyThreshold   int    `json:"healthyThreshold"`
	UnhealthyThreshold int    `json:"unhealthyThreshold"`
	Interval           int    `json:"interval"`
	Timeout            int    `json:"timeout"`
}

type groupBody struct {
	Name        string    `json:"name"`
	Region      string    `json:"region"`
	Type        string    `json:"type"`
	VPCID       string    `json:"vpcId"`
	TrafficDial int       `json:"trafficDial"`
	Members     []rowBody `json:"members"`
}

type rowBody struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	SubnetID    string `json:"subnetId"`
	Port        int    `json:"port"`
	MonitorPort int    `json:"monitorPort"`
	Weight      int    `json:"weight"`
	BackupRole  bool   `json:"backupRole"`
}

type listenerBody struct {
	Name              string `json:"name"`
	Protocol          string `json:"protocol"`
	Port              int    `json:"port"`
	GlobalPoolID      string `json:"globalPoolId"`
	AllowedCIDRs      string `json:"allowedCidrs"`
	TimeoutClient     int    `json:"timeoutClient"`
	TimeoutConnection int    `json:"timeoutConnection"`
	TimeoutMember     int    `json:"timeoutMember"`
}

func (p wirePool) public() Pool {
	out := Pool{ID: p.ID, Name: p.Name, GlobalLoadBalancerID: p.GlobalLoadBalancerID, Algorithm: p.Algorithm, Protocol: p.Protocol, Status: p.Status}
	if h := p.Health; h != nil {
		out.Health = &PoolHealthMonitor{ID: h.ID, GlobalPoolID: h.GlobalPoolID, GlobalLoadBalancerID: h.GlobalLoadBalancerID, Protocol: h.Protocol, Timeout: h.Timeout, IntervalTime: h.IntervalTime, HealthyThreshold: h.HealthyThreshold, UnhealthyThreshold: h.UnhealthyThreshold, Status: h.Status}
	}
	return out
}

func (g wireGroup) public() PoolMember {
	out := PoolMember{ID: g.ID, Name: g.Name, Region: g.Region, GlobalPoolID: g.GlobalPoolID, GlobalLoadBalancerID: g.GlobalLoadBalancerID, TrafficDial: g.TrafficDial, VPCID: g.VPCID, Type: g.Type, Status: g.Status}
	for _, r := range g.Members {
		out.Members = append(out.Members, PoolMemberDetail{ID: r.ID, Name: r.Name, GlobalPoolMemberID: r.GlobalPoolMemberID, GlobalLoadBalancerID: r.GlobalLoadBalancerID, SubnetID: r.SubnetID, Address: r.Address, Weight: r.Weight, Port: r.Port, MonitorPort: r.MonitorPort, BackupRole: r.BackupRole != nil && *r.BackupRole, Status: r.Status})
	}
	return out
}

func (l wireListener) public() Listener {
	return Listener{ID: l.ID, Name: l.Name, Protocol: l.Protocol, Port: l.Port, GlobalLoadBalancerID: l.GlobalLoadBalancerID, GlobalPoolID: l.GlobalPoolID, TimeoutClient: l.TimeoutClient, TimeoutConnection: l.TimeoutConnection, TimeoutMember: l.TimeoutMember, AllowedCIDRs: l.AllowedCIDRs, Status: l.Status}
}

func (p wirePool) rowID() string     { return p.ID }
func (g wireGroup) rowID() string    { return g.ID }
func (l wireListener) rowID() string { return l.ID }
