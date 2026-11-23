package vks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

// ErrNotSettled means the write outcome is not confirmed.
var ErrNotSettled = core.ErrNotSettled

// ErrFailed means the cluster reported a terminal failure.
var ErrFailed = core.ErrFailed

// CreateClusterInput describes a private cluster with one private node group.
type CreateClusterInput struct {
	// Name is the cluster name.
	Name string `json:"name" vngcloud:"required"`
	// Version is the Kubernetes version.
	Version string `json:"version" vngcloud:"required"`
	// VPCID is the existing VPC ID.
	VPCID string `json:"vpcId" vngcloud:"required"`
	// SubnetID is the existing subnet ID.
	SubnetID string `json:"subnetId" vngcloud:"required"`
	// PodCIDR is the pod network CIDR.
	PodCIDR string `json:"podCidr" vngcloud:"required"`
	// NodeGroupName is the bootstrap node group name.
	NodeGroupName string `json:"nodeGroupName" vngcloud:"required"`
	// NumNodes is the number of nodes, from one to ten.
	NumNodes int `json:"numNodes" vngcloud:"required"`
	// FlavorID is the node flavor ID.
	FlavorID string `json:"flavorId" vngcloud:"required"`
	// OS is the node operating system.
	OS string `json:"os" vngcloud:"required"`
	// DiskSize is the node disk size in GiB.
	DiskSize int `json:"diskSize" vngcloud:"required"`
	// DiskTypeID is the disk type ID.
	DiskTypeID string `json:"diskTypeId" vngcloud:"required"`
	// SecurityGroupIDs are the node security group IDs.
	SecurityGroupIDs []string `json:"securityGroupIds" vngcloud:"required"`
	// SSHKeyID is the existing SSH key ID.
	SSHKeyID string `json:"sshKeyId" vngcloud:"required"`
	// NoWait returns after acceptance without waiting for provisioned status.
	NoWait bool `json:"noWait"`
}

// CreateClusterOutput holds the accepted cluster ID and last observed status.
type CreateClusterOutput struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// DeleteClusterInput identifies the cluster to delete.
type DeleteClusterInput struct {
	// ClusterID is the exact cluster ID.
	ClusterID string `json:"clusterId" vngcloud:"required"`
	// NoWait returns after acceptance without confirming absence.
	NoWait bool `json:"noWait"`
}

// DeleteClusterOutput reports confirmed absence.
type DeleteClusterOutput struct {
	Deleted bool `json:"deleted"`
}

var clusterNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{3,18}[a-z0-9]$`)
var groupNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{3,13}[a-z0-9]$`)
var clusterStatusPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_-]{0,63}$`)
var unsafeProviderMessage = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|(?:access|refresh)?token|authorization|bearer|credential|cookie|session|api[-_ ]?key)\b\s*(?:[:=]|\bis\b)\s*[^\s,;]+|\bbearer\s+[^\s,;]+|\b[a-z][a-z0-9]*-[a-z0-9]*[0-9][a-z0-9-]*\b|\b[0-9]{8,}\b|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[\w.+-]+@[\w.-]+\.[a-z]{2,}|[\x00-\x1f\x7f])`)
var providerCredentialPattern = regexp.MustCompile(`(?i)bearer|token|password|secret|credential|authorization|api[ _-]?key`)

func (c *Client) clusterURL(id string, q url.Values) string {
	parts := []string{"clusters"}
	if id != "" {
		parts = append(parts, id)
	}
	return c.c.RouteURL(routes.Route{Product: routes.ProductVKS, Version: "v1", Parts: parts, Query: q})
}

func (c *Client) clusterRead(ctx context.Context, id string) (*CreateClusterOutput, error) {
	const op = "vks.GetCluster"
	var out CreateClusterOutput
	_, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: http.MethodGet, URL: c.clusterURL(id, nil), OK: []int{200}, Sensitive: true, NoRedirect: true, WithholdMessage: "VKS response withheld"}, &out)
	if err != nil {
		return nil, safeError(op, err)
	}
	if out.ID != id || !clusterStatusPattern.MatchString(out.Status) {
		return nil, malformed(op)
	}
	return &out, nil
}

func privatePodCIDR(prefix netip.Prefix) bool {
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		block := netip.MustParsePrefix(cidr)
		if prefix.Bits() >= block.Bits() && block.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) waitCluster(ctx context.Context, read func(context.Context) (bool, error)) error {
	const bound = 30 * time.Minute
	const interval = 10 * time.Second
	deadline := c.now().Add(bound)
	wait, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !c.now().Before(deadline) || wait.Err() != nil {
			return context.DeadlineExceeded
		}
		return nil
	}
	for {
		if err := check(); err != nil {
			return unsettled(err)
		}
		done, err := read(wait)
		if checkErr := check(); checkErr != nil {
			return unsettled(checkErr)
		}
		if err != nil {
			if errors.Is(err, ErrFailed) {
				return err
			}
			return unsettled(err)
		}
		if done {
			return nil
		}
		if err := c.sleep(wait, min(interval, deadline.Sub(c.now()))); err != nil {
			return unsettled(err)
		}
	}
}

func unsettled(err error) error {
	if err == nil {
		return ErrNotSettled
	}
	return errors.Join(ErrNotSettled, err)
}

func createRefusalMessage(body []byte, credentials, ids []string) string {
	const withheld = "provider response withheld"
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return withheld
	}
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" || len(message) > 512 || providerCredentialPattern.MatchString(message) || unsafeProviderMessage.MatchString(message) || transport.RedactErrorText(message, credentials...) != message {
		return withheld
	}
	for _, r := range message {
		if r < 32 || r > 126 {
			return withheld
		}
	}
	for _, id := range ids {
		if id != "" {
			message = strings.ReplaceAll(message, id, "[redacted]")
		}
	}
	return message
}

func (c *Client) writeProvenance() error {
	expected := endpoints.ResolveIAMUser(c.c.Region(), endpoints.Overrides{})
	if !c.c.UsesIAMUserLogin() || !c.c.UsesDefaultAuthEndpoints() || c.c.Endpoint(routes.ProductVKS) != expected.VKS {
		return fmt.Errorf("%w: VKS writes require SDK IAM-user login and default endpoints", core.ErrInvalidConfig)
	}
	return nil
}

func invalidClusterField(op, field, rule string) error {
	return fmt.Errorf("%w: %s: %s %s", core.ErrInvalidInput, op, field, rule)
}

func checkClusterID(op, field, id string) error {
	if core.CheckPathID(op, field, id) != nil {
		return invalidClusterField(op, field, "must be non-empty and contain only letters, digits, and hyphens")
	}
	return nil
}

// CreateCluster submits a single private cluster order and optionally waits for ACTIVE.
func (c *Client) CreateCluster(ctx context.Context, in *CreateClusterInput) (*CreateClusterOutput, error) {
	const op = "vks.CreateCluster"
	if err := c.validate(); err != nil {
		return nil, err
	}
	if err := c.writeProvenance(); err != nil {
		return nil, err
	}
	if in == nil {
		return nil, invalidClusterField(op, "input", "must be non-nil")
	}
	switch {
	case !clusterNamePattern.MatchString(in.Name):
		return nil, invalidClusterField(op, "Name", "must be 5 to 20 lowercase letters, digits, or hyphens, starting and ending with a letter or digit")
	case !groupNamePattern.MatchString(in.NodeGroupName):
		return nil, invalidClusterField(op, "NodeGroupName", "must be 5 to 15 lowercase letters, digits, or hyphens, starting and ending with a letter or digit")
	case in.Version == "":
		return nil, invalidClusterField(op, "Version", "must be non-empty")
	case in.OS == "":
		return nil, invalidClusterField(op, "OS", "must be non-empty")
	case in.NumNodes < 1 || in.NumNodes > 10:
		return nil, invalidClusterField(op, "NumNodes", "must be 1 to 10")
	case in.DiskSize <= 0:
		return nil, invalidClusterField(op, "DiskSize", "must be positive")
	case len(in.SecurityGroupIDs) == 0:
		return nil, invalidClusterField(op, "SecurityGroupIDs", "must contain at least one ID")
	}
	prefix, err := netip.ParsePrefix(in.PodCIDR)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || !privatePodCIDR(prefix) {
		return nil, invalidClusterField(op, "PodCIDR", "must be a canonical private IPv4 CIDR")
	}
	ids := []struct{ field, value string }{
		{"VPCID", in.VPCID}, {"SubnetID", in.SubnetID}, {"FlavorID", in.FlavorID},
		{"DiskTypeID", in.DiskTypeID}, {"SSHKeyID", in.SSHKeyID},
	}
	for _, id := range ids {
		if err := checkClusterID(op, id.field, id.value); err != nil {
			return nil, err
		}
	}
	for _, id := range in.SecurityGroupIDs {
		if err := checkClusterID(op, "SecurityGroupIDs", id); err != nil {
			return nil, err
		}
	}
	body := map[string]any{"name": in.Name, "version": in.Version, "releaseChannel": "STABLE", "enablePrivateCluster": true, "networkType": "CILIUM_OVERLAY", "vpcId": in.VPCID, "cidr": in.PodCIDR, "azStrategy": "SINGLE", "listSubnetIds": []string{in.SubnetID}, "enabledLoadBalancerPlugin": false, "enabledBlockStoreCsiPlugin": false, "nodeGroup": map[string]any{"name": in.NodeGroupName, "numNodes": in.NumNodes, "os": in.OS, "flavorId": in.FlavorID, "diskSize": in.DiskSize, "diskType": in.DiskTypeID, "enablePrivateNodes": true, "securityGroups": in.SecurityGroupIDs, "sshKeyId": in.SSHKeyID, "subnetId": in.SubnetID, "upgradeConfig": map[string]any{"strategy": "SURGE"}}}
	out := &CreateClusterOutput{}
	var credentials []string
	var notSent bool
	status, _, response, err := c.c.DoRaw(ctx, transport.Request{Operation: op, Method: http.MethodPost, URL: c.clusterURL("", url.Values{"autoRenewal": {"false"}, "poc": {"false"}}), Body: body, OK: []int{200, 202}, Once: true, NoRedirect: true, Sensitive: true, NotSent: &notSent, SentCredentials: &credentials, WithholdMessage: "VKS response withheld"})
	if err != nil {
		if notSent {
			return nil, err
		}
		return out, unsettled(safeError(op, err))
	}
	if status >= 400 && status < 500 && status != http.StatusRequestTimeout {
		safeErr := safeError(op, &core.APIError{StatusCode: status})
		var refusal *core.APIError
		if !errors.As(safeErr, &refusal) {
			return nil, safeErr
		}
		knownIDs := []string{in.VPCID, in.SubnetID, in.FlavorID, in.DiskTypeID, in.SSHKeyID}
		knownIDs = append(knownIDs, in.SecurityGroupIDs...)
		refusal.Message = createRefusalMessage(response, credentials, knownIDs)
		return nil, refusal
	}
	if status != http.StatusOK && status != http.StatusAccepted {
		return out, unsettled(nil)
	}
	if json.Unmarshal(response, out) != nil {
		if core.CheckPathID(op, "ID", out.ID) != nil {
			out.ID = ""
		}
		out.Status = ""
		return out, unsettled(nil)
	}
	if err := core.CheckPathID(op, "ID", out.ID); err != nil {
		out.ID = ""
		return out, unsettled(nil)
	}
	if out.Status != "" && !clusterStatusPattern.MatchString(out.Status) {
		out.Status = ""
		return out, unsettled(nil)
	}
	if in.NoWait {
		return out, nil
	}
	err = c.waitCluster(ctx, func(wait context.Context) (bool, error) {
		current, readErr := c.clusterRead(wait, out.ID)
		if readErr != nil {
			return false, readErr
		}
		out.Status = current.Status
		status := strings.ToUpper(out.Status)
		if status == "ERROR" || strings.Contains(status, "FAIL") {
			return false, ErrFailed
		}
		return status == "ACTIVE", nil
	})
	return out, err
}

// DeleteCluster sends one delete after reading the exact cluster.
func (c *Client) DeleteCluster(ctx context.Context, in *DeleteClusterInput) (*DeleteClusterOutput, error) {
	const op = "vks.DeleteCluster"
	if err := c.validate(); err != nil {
		return nil, err
	}
	if err := c.writeProvenance(); err != nil {
		return nil, err
	}
	if in == nil {
		return nil, invalidClusterField(op, "input", "must be non-nil")
	}
	if err := checkClusterID(op, "ClusterID", in.ClusterID); err != nil {
		return nil, err
	}
	if _, err := c.clusterRead(ctx, in.ClusterID); err != nil {
		return nil, err
	}
	out := &DeleteClusterOutput{}
	_, err := c.c.DoJSONStatus(ctx, transport.Request{Operation: op, Method: http.MethodDelete, URL: c.clusterURL(in.ClusterID, nil), OK: []int{200, 202}, Once: true, NoRedirect: true, Sensitive: true, AllowEmptyBody: true, WithholdMessage: "VKS response withheld"}, nil)
	if err != nil {
		return out, unsettled(safeError(op, err))
	}
	if in.NoWait {
		return out, nil
	}
	err = c.waitCluster(ctx, func(wait context.Context) (bool, error) {
		_, readErr := c.clusterRead(wait, in.ClusterID)
		if errors.Is(readErr, core.ErrNotFound) {
			return true, nil
		}
		return false, readErr
	})
	if err == nil {
		out.Deleted = true
	}
	return out, err
}
