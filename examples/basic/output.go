package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"danny.vn/vngcloud"
)

const outputRoot = "examples/basic/output"

var operationOutputPaths = map[string]string{
	"ListProjects":                               "project/project",
	"Portal.GetUserInfo":                         "portal/user_info",
	"Portal.ListZones":                           "portal/zone",
	"Portal.ListQuotaUsed":                       "portal/quota_used",
	"Portal.GetQuota":                            "portal/quota_detail",
	"Portal.GetTagQuota":                         "portal/tag_quota",
	"Compute.ListServers":                        "server/instance",
	"Compute.ListSSHKeys":                        "server/ssh_key",
	"Compute.ListServerGroups":                   "server/placement_group",
	"Compute.ListServerSecurityGroups":           "server/instance_security_group",
	"Compute.ListServerGroupMembers":             "server/placement_group_member",
	"Compute.ListServerGroupPolicies":            "server/placement_group_policy",
	"Compute.ListOSImages":                       "server/system_image_os",
	"Compute.ListGPUImages":                      "server/system_image_gpu",
	"Compute.ListUserImages":                     "server/user_image",
	"Volume.ListVolumes":                         "volume/volume",
	"Volume.GetUnderlyingVolume":                 "volume/underlying_volume",
	"Volume.GetDefaultVolumeType":                "volume/default_type",
	"Volume.GetVolumeType":                       "volume/type_detail",
	"Volume.ListVolumeTypeZones":                 "volume/type_zone",
	"Volume.ListVolumeTypes":                     "volume/type",
	"Volume.ListEncryptionTypes":                 "volume/encryption_type",
	"Volume.ListSnapshots":                       "volume/snapshot",
	"Network.ListVPCs":                           "network/vpc",
	"Network.GetVPC":                             "network/vpc_detail",
	"Network.ListVNetworkRegions":                "network/vnetwork_region",
	"Network.ListSubnetsByVPC":                   "network/subnet",
	"Network.GetSubnet":                          "network/subnet_detail",
	"Network.ListWANIPs":                         "network/floating_ip",
	"Network.ListNetworkInterfaces":              "network/interface",
	"Network.ListSecurityGroups":                 "network/security_group",
	"Network.GetSecurityGroup":                   "network/security_group_detail",
	"Network.ListServersBySecurityGroup":         "network/security_group_server",
	"Network.ListVirtualIPAddresses":             "network/virtual_ip",
	"Network.ListRouteTables":                    "network/route_table",
	"Network.ListRouteTableRoutes":               "network/route_table_route",
	"Network.ListPeerings":                       "network/peering",
	"Network.ListNetworkACLs":                    "network/network_acl",
	"Network.ListSecurityGroupRules":             "network/security_group_rule",
	"Network.ListInterconnects":                  "network/interconnect",
	"Network.ListEndpoints":                      "network/endpoint",
	"Network.GetEndpoint":                        "network/endpoint_detail",
	"Network.ListEndpointTags":                   "network/endpoint_tag",
	"Network.GetVirtualIPAddress":                "network/virtual_ip_detail",
	"Network.ListAddressPairsByVirtualIPAddress": "network/virtual_ip_address_pair",
	"Network.ListAddressPairsByVirtualSubnet":    "network/virtual_subnet_address_pair",
	"LoadBalancer.ListLoadBalancers":             "loadbalancer/load_balancer",
	"LoadBalancer.GetLoadBalancer":               "loadbalancer/load_balancer_detail",
	"LoadBalancer.ListListeners":                 "loadbalancer/listener",
	"LoadBalancer.GetListener":                   "loadbalancer/listener_detail",
	"LoadBalancer.ListPools":                     "loadbalancer/pool",
	"LoadBalancer.GetPool":                       "loadbalancer/pool_detail",
	"LoadBalancer.GetPoolHealthMonitor":          "loadbalancer/pool_health_monitor",
	"LoadBalancer.ListPoolMembers":               "loadbalancer/pool_member",
	"LoadBalancer.ListPolicies":                  "loadbalancer/policy",
	"LoadBalancer.GetPolicy":                     "loadbalancer/policy_detail",
	"LoadBalancer.ListTags":                      "loadbalancer/tag",
	"LoadBalancer.ListLoadBalancerPackages":      "loadbalancer/package",
	"LoadBalancer.ListCertificates":              "loadbalancer/certificate",
	"LoadBalancer.GetCertificate":                "loadbalancer/certificate_detail",
	"GLB.ListLoadBalancers":                      "glb/load_balancer",
	"GLB.GetLoadBalancer":                        "glb/load_balancer_detail",
	"GLB.ListPools":                              "glb/pool",
	"GLB.ListListeners":                          "glb/listener",
	"GLB.GetListener":                            "glb/listener_detail",
	"GLB.ListPoolMembers":                        "glb/pool_member",
	"GLB.GetPoolMember":                          "glb/pool_member_detail",
	"GLB.ListUsageHistories":                     "glb/usage_history",
	"GLB.ListPackages":                           "glb/package",
	"GLB.ListRegions":                            "glb/region",
	"DNS.ListHostedZones":                        "dns/hosted_zone",
	"DNS.GetHostedZone":                          "dns/hosted_zone_detail",
	"DNS.ListRecords":                            "dns/record",
	"DNS.GetRecord":                              "dns/record_detail",
	"VCR.ListRepositories":                       "containerregistry/repository",
	"VCR.ListUsers":                              "containerregistry/user",
}

type rawCaptureStore struct {
	resources map[string]*rawResourceOutput
}

type rawResourceOutput struct {
	Regions []rawRegionOutput `json:"regions"`
}

type rawRegionOutput struct {
	Config     string          `json:"config"`
	Region     string          `json:"region"`
	Operation  string          `json:"operation"`
	Method     string          `json:"method"`
	URL        string          `json:"url"`
	StatusCode int             `json:"statusCode"`
	Body       json.RawMessage `json:"body,omitempty"`
	BodyText   string          `json:"bodyText,omitempty"`
}

type sdkOutputStore struct {
	config    string
	resources map[string]*sdkResourceOutput
}

type sdkResourceOutput struct {
	Regions []sdkRegionOutput `json:"regions"`
}

type sdkRegionOutput struct {
	Config          string `json:"config"`
	Region          string `json:"region"`
	ProjectSelected bool   `json:"projectSelected"`
	Scope           string `json:"scope,omitempty"`
	Count           int    `json:"count"`
	Items           any    `json:"items,omitempty"`
	Error           string `json:"error,omitempty"`
}

func newRawCaptureStore() *rawCaptureStore {
	return &rawCaptureStore{resources: map[string]*rawResourceOutput{}}
}

func (s *rawCaptureStore) add(config, region string, captured vngcloud.ResponseCapture) {
	path, ok := operationOutputPaths[captured.Operation]
	if !ok {
		return
	}
	resource := s.resources[path]
	if resource == nil {
		resource = &rawResourceOutput{}
		s.resources[path] = resource
	}
	item := rawRegionOutput{
		Config:     config,
		Region:     region,
		Operation:  captured.Operation,
		Method:     captured.Method,
		URL:        captured.URL,
		StatusCode: captured.StatusCode,
	}
	if json.Valid(captured.Body) {
		item.Body = append(json.RawMessage(nil), captured.Body...)
	} else {
		item.BodyText = string(captured.Body)
	}
	resource.Regions = append(resource.Regions, item)
}

func (s *rawCaptureStore) writeAll() error {
	return writeResources(filepath.Join(outputRoot, "raw"), s.resources)
}

func newSDKOutputStore() *sdkOutputStore {
	return &sdkOutputStore{resources: map[string]*sdkResourceOutput{}}
}

func (s *sdkOutputStore) setConfig(config string) {
	s.config = config
}

func (s *sdkOutputStore) add(path string, client *vngcloud.Client, items any, err error) {
	s.addWithScope(path, client, "region", items, err)
}

func (s *sdkOutputStore) addGlobal(path string, client *vngcloud.Client, items any, err error) {
	s.addWithScope(path, client, "global", items, err)
}

func (s *sdkOutputStore) addWithScope(path string, client *vngcloud.Client, scope string, items any, err error) {
	resource := s.resources[path]
	if resource == nil {
		resource = &sdkResourceOutput{}
		s.resources[path] = resource
	}

	item := sdkRegionOutput{
		Config:          s.config,
		Region:          client.Region(),
		ProjectSelected: client.ProjectID() != "",
		Scope:           scope,
		Count:           countItems(items),
		Items:           items,
	}
	if err != nil {
		item.Error = err.Error()
		item.Items = nil
		item.Count = 0
	}
	resource.Regions = append(resource.Regions, item)
}

func (s *sdkOutputStore) writeAll() error {
	return writeResources(filepath.Join(outputRoot, "sdk"), s.resources)
}

func writeResources[T any](root string, resources map[string]*T) error {
	paths := make([]string, 0, len(resources))
	for path := range resources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := writeJSON(filepath.Join(root, path+".json"), resources[path]); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func countItems(items any) int {
	if items == nil {
		return 0
	}
	value := reflect.ValueOf(items)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Array, reflect.Slice:
		return value.Len()
	default:
		return 1
	}
}

func printResult(label string, count int, err error) {
	if err != nil {
		fmt.Printf("%s: error\n", label)
		return
	}
	fmt.Printf("%s: %d\n", label, count)
}
