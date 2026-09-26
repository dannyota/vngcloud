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
	"project.ListProjects":                       "project/project",
	"portal.GetUserInfo":                         "portal/user_info",
	"portal.ListZones":                           "portal/zone",
	"portal.ListQuotaUsed":                       "portal/quota_used",
	"portal.GetQuota":                            "portal/quota_detail",
	"portal.GetTagQuota":                         "portal/tag_quota",
	"compute.ListServers":                        "server/instance",
	"compute.ListSSHKeys":                        "server/ssh_key",
	"compute.ListServerGroups":                   "server/placement_group",
	"compute.ListServerSecurityGroups":           "server/instance_security_group",
	"compute.ListServerGroupMembers":             "server/placement_group_member",
	"compute.ListServerGroupPolicies":            "server/placement_group_policy",
	"compute.ListOSImages":                       "server/system_image_os",
	"compute.ListGPUImages":                      "server/system_image_gpu",
	"compute.ListUserImages":                     "server/user_image",
	"volume.ListVolumes":                         "volume/volume",
	"volume.GetUnderlyingVolume":                 "volume/underlying_volume",
	"volume.GetDefaultVolumeType":                "volume/default_type",
	"volume.GetVolumeType":                       "volume/type_detail",
	"volume.ListVolumeTypeZones":                 "volume/type_zone",
	"volume.ListVolumeTypes":                     "volume/type",
	"volume.ListEncryptionTypes":                 "volume/encryption_type",
	"volume.ListSnapshots":                       "volume/snapshot",
	"network.ListVPCs":                           "network/vpc",
	"network.GetVPC":                             "network/vpc_detail",
	"network.ListVNetworkRegions":                "network/vnetwork_region",
	"network.ListSubnetsByVPC":                   "network/subnet",
	"network.GetSubnet":                          "network/subnet_detail",
	"network.ListWANIPs":                         "network/floating_ip",
	"network.ListNetworkInterfaces":              "network/interface",
	"network.ListSecurityGroups":                 "network/security_group",
	"network.GetSecurityGroup":                   "network/security_group_detail",
	"network.ListServersBySecurityGroup":         "network/security_group_server",
	"network.ListVirtualIPAddresses":             "network/virtual_ip",
	"network.ListRouteTables":                    "network/route_table",
	"network.ListRouteTableRoutes":               "network/route_table_route",
	"network.ListPeerings":                       "network/peering",
	"network.ListNetworkACLs":                    "network/network_acl",
	"network.ListSecurityGroupRules":             "network/security_group_rule",
	"network.ListInterconnects":                  "network/interconnect",
	"network.ListEndpoints":                      "network/endpoint",
	"network.GetEndpoint":                        "network/endpoint_detail",
	"network.ListEndpointTags":                   "network/endpoint_tag",
	"network.GetVirtualIPAddress":                "network/virtual_ip_detail",
	"network.ListAddressPairsByVirtualIPAddress": "network/virtual_ip_address_pair",
	"network.ListAddressPairsByVirtualSubnet":    "network/virtual_subnet_address_pair",
	"loadbalancer.ListLoadBalancers":             "loadbalancer/load_balancer",
	"loadbalancer.GetLoadBalancer":               "loadbalancer/load_balancer_detail",
	"loadbalancer.ListListeners":                 "loadbalancer/listener",
	"loadbalancer.GetListener":                   "loadbalancer/listener_detail",
	"loadbalancer.ListPools":                     "loadbalancer/pool",
	"loadbalancer.GetPool":                       "loadbalancer/pool_detail",
	"loadbalancer.GetPoolHealthMonitor":          "loadbalancer/pool_health_monitor",
	"loadbalancer.ListPoolMembers":               "loadbalancer/pool_member",
	"loadbalancer.ListPolicies":                  "loadbalancer/policy",
	"loadbalancer.GetPolicy":                     "loadbalancer/policy_detail",
	"loadbalancer.ListTags":                      "loadbalancer/tag",
	"loadbalancer.ListPackages":                  "loadbalancer/package",
	"loadbalancer.ListCertificates":              "loadbalancer/certificate",
	"loadbalancer.GetCertificate":                "loadbalancer/certificate_detail",
	"globalloadbalancer.ListLoadBalancers":       "glb/load_balancer",
	"globalloadbalancer.GetLoadBalancer":         "glb/load_balancer_detail",
	"globalloadbalancer.ListPools":               "glb/pool",
	"globalloadbalancer.ListListeners":           "glb/listener",
	"globalloadbalancer.GetListener":             "glb/listener_detail",
	"globalloadbalancer.ListPoolMembers":         "glb/pool_member",
	"globalloadbalancer.GetPoolMember":           "glb/pool_member_detail",
	"globalloadbalancer.ListUsageHistories":      "glb/usage_history",
	"globalloadbalancer.ListPackages":            "glb/package",
	"globalloadbalancer.ListRegions":             "glb/region",
	"dns.ListHostedZones":                        "dns/hosted_zone",
	"dns.GetHostedZone":                          "dns/hosted_zone_detail",
	"dns.ListRecords":                            "dns/record",
	"dns.GetRecord":                              "dns/record_detail",
	"containerregistry.ListRepositories":         "containerregistry/repository",
	"containerregistry.ListUsers":                "containerregistry/user",
	"billing.ListBudgets":                        "billing/budget",
	"billing.GetCurrentPeriodCost":               "billing/current_period_cost",
	"billing.GetBalances":                        "billing/balance",
	"pricing.GetQuote":                           "pricing/quote",
	"monitor.ListChecks":                         "monitor/check",
	"monitor.GetCheck":                           "monitor/check_detail",
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

func (s *sdkOutputStore) add(path string, cfg vngcloud.Config, items any, err error) {
	s.addWithScope(path, cfg, "region", items, err)
}

func (s *sdkOutputStore) addGlobal(path string, cfg vngcloud.Config, items any, err error) {
	s.addWithScope(path, cfg, "global", items, err)
}

// addWithScope records a region- or project-scoped call. projectSelected
// approximates whether the shared Config resolved a project: a project-
// scoped call fails before this point when the project cannot be resolved,
// so success implies a project was selected.
func (s *sdkOutputStore) addWithScope(path string, cfg vngcloud.Config, scope string, items any, err error) {
	s.record(path, cfg.Region(), err == nil, scope, items, err)
}

// addAccount records billing, which ignores the configured region and has no
// project; region is always "".
func (s *sdkOutputStore) addAccount(path string, items any, err error) {
	s.record(path, "", false, "account", items, err)
}

// addPricing records pricing, which is scoped to region but has no project.
func (s *sdkOutputStore) addPricing(path, region string, items any, err error) {
	s.record(path, region, false, "region", items, err)
}

func (s *sdkOutputStore) record(path, region string, projectSelected bool, scope string, items any, err error) {
	resource := s.resources[path]
	if resource == nil {
		resource = &sdkResourceOutput{}
		s.resources[path] = resource
	}

	item := sdkRegionOutput{
		Config:          s.config,
		Region:          region,
		ProjectSelected: projectSelected,
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
