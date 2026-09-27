package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// createSecurityGroupRuleBody is CreateSecurityGroupRule's request body.
// The reference does not document securityGroupId in it, but both VNG
// Cloud's own SDK and its Terraform provider send it, so this SDK does too.
type createSecurityGroupRuleBody struct {
	Direction       string `json:"direction"`
	EtherType       string `json:"etherType"`
	Protocol        string `json:"protocol"`
	PortRangeMin    int    `json:"portRangeMin"`
	PortRangeMax    int    `json:"portRangeMax"`
	RemoteIPPrefix  string `json:"remoteIpPrefix"`
	Description     string `json:"description"`
	SecurityGroupID string `json:"securityGroupId"`
}

// createSecurityGroupRuleResponse is Create's response, confirmed live:
// the object is wrapped in a "data" field, and inside it does not match
// the SecurityGroupRule read model: the rule's own id comes back as uuid,
// its group as secgroupUuid, and ruleId is an integer. Decoding straight
// into SecurityGroupRule would fail on the integer ruleId, so
// CreateSecurityGroupRule decodes into this private type and maps it
// instead. The rule is ACTIVE in this same response; CreateSecurityGroupRule
// takes no post-create wait of its own.
type createSecurityGroupRuleResponse struct {
	Data struct {
		UUID         string `json:"uuid"`
		SecgroupUUID string `json:"secgroupUuid"`
		RuleID       int    `json:"ruleId"`
	} `json:"data"`
}

// CreateSecurityGroupRuleInput creates a rule in a security group.
//
// RemoteIPPrefix is required and must parse with netip.ParsePrefix: a bare
// address with no prefix length is refused, so a single host is written
// "/32" or "/128". EtherType left empty is derived from RemoteIPPrefix's
// address family (IPv4 or IPv6); given explicitly, it must match that
// family. PortRangeMin and PortRangeMax each range 0 to 65535;
// PortRangeMax left 0 sends the same value as PortRangeMin, so a single
// port needs only PortRangeMin, and PortRangeMin must not be above
// PortRangeMax. For Protocol tcp or udp (in any case), PortRangeMin must be
// at least 1: 0 is not a valid tcp or udp port, and "all ports" is written
// PortRangeMin 1, PortRangeMax 65535.
//
// The SDK never defaults Protocol or RemoteIPPrefix; both must be set.
// Protocol and Direction are sent to the server as given and are checked
// only for shape, not against a fixed value set, so a protocol or
// direction the server adds later never needs an SDK release.
type CreateSecurityGroupRuleInput struct {
	SecurityGroupID string `vngcloud:"required"`
	Direction       string `vngcloud:"required"`
	Protocol        string `vngcloud:"required"`
	RemoteIPPrefix  string `vngcloud:"required"`

	EtherType    string
	PortRangeMin int
	PortRangeMax int
	Description  string
}

type CreateSecurityGroupRuleOutput struct {
	SecurityGroupRule SecurityGroupRule
}

// CreateSecurityGroupRule creates a rule in a security group. A duplicate
// rule fails with the server's own SecurityGroupRuleExists message.
// Confirmed live, the rule is already ACTIVE in the create response, so
// CreateSecurityGroupRule takes no post-create wait.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the rule may exist, and the caller lists the
// group's rules with ListSecurityGroupRules before creating it again,
// rather than retrying blind.
func (c *Client) CreateSecurityGroupRule(ctx context.Context, in *CreateSecurityGroupRuleInput) (*CreateSecurityGroupRuleOutput, error) {
	const op = "network.CreateSecurityGroupRule"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SecurityGroupID", in.SecurityGroupID); err != nil {
		return nil, err
	}

	etherType, portMax, err := checkSecurityGroupRuleShape(op, in)
	if err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp createSecurityGroupRuleResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID, "secgroupRules"}, nil),
		Body: createSecurityGroupRuleBody{
			Direction:       in.Direction,
			EtherType:       etherType,
			Protocol:        in.Protocol,
			PortRangeMin:    in.PortRangeMin,
			PortRangeMax:    portMax,
			RemoteIPPrefix:  in.RemoteIPPrefix,
			Description:     in.Description,
			SecurityGroupID: in.SecurityGroupID,
		},
		OK: []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousSecurityGroupRuleCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	return &CreateSecurityGroupRuleOutput{SecurityGroupRule: SecurityGroupRule{
		ID:              resp.Data.UUID,
		SecurityGroupID: in.SecurityGroupID,
		Direction:       in.Direction,
		EtherType:       etherType,
		Protocol:        in.Protocol,
		PortRangeMin:    in.PortRangeMin,
		PortRangeMax:    portMax,
		RemoteIPPrefix:  in.RemoteIPPrefix,
		Description:     in.Description,
	}}, nil
}

// checkSecurityGroupRuleShape checks in's shape per
// CreateSecurityGroupRuleInput's doc comment, before any request, and
// returns the EtherType and PortRangeMax to send: EtherType derived from
// RemoteIPPrefix's family when in.EtherType is empty, and PortRangeMax
// defaulted to PortRangeMin when in.PortRangeMax is 0.
func checkSecurityGroupRuleShape(op string, in *CreateSecurityGroupRuleInput) (etherType string, portMax int, err error) {
	prefix, err := netip.ParsePrefix(in.RemoteIPPrefix)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %s: RemoteIPPrefix must be a CIDR prefix such as 203.0.113.0/24 or 2001:db8::/32, got %q",
			core.ErrInvalidInput, op, in.RemoteIPPrefix)
	}
	family := "IPv4"
	if prefix.Addr().Is6() {
		family = "IPv6"
	}
	etherType = in.EtherType
	switch {
	case etherType == "":
		etherType = family
	case etherType != family:
		return "", 0, fmt.Errorf("%w: %s: EtherType %q does not match RemoteIPPrefix %s's family %s",
			core.ErrInvalidInput, op, in.EtherType, in.RemoteIPPrefix, family)
	}

	if in.PortRangeMin < 0 || in.PortRangeMin > 65535 {
		return "", 0, fmt.Errorf("%w: %s: PortRangeMin must be 0 to 65535, got %d", core.ErrInvalidInput, op, in.PortRangeMin)
	}
	if in.PortRangeMax < 0 || in.PortRangeMax > 65535 {
		return "", 0, fmt.Errorf("%w: %s: PortRangeMax must be 0 to 65535, got %d", core.ErrInvalidInput, op, in.PortRangeMax)
	}
	portMax = in.PortRangeMax
	if portMax == 0 {
		portMax = in.PortRangeMin
	}
	if in.PortRangeMin > portMax {
		return "", 0, fmt.Errorf("%w: %s: PortRangeMin %d is above PortRangeMax %d", core.ErrInvalidInput, op, in.PortRangeMin, portMax)
	}
	if isTCPOrUDP(in.Protocol) && in.PortRangeMin < 1 {
		return "", 0, fmt.Errorf("%w: %s: Protocol %s requires PortRangeMin at least 1; write \"all ports\" as PortRangeMin 1, PortRangeMax 65535",
			core.ErrInvalidInput, op, in.Protocol)
	}
	return etherType, portMax, nil
}

// isTCPOrUDP reports whether protocol is "tcp" or "udp" in any case.
func isTCPOrUDP(protocol string) bool {
	return strings.EqualFold(protocol, "tcp") || strings.EqualFold(protocol, "udp")
}

// wrapAmbiguousSecurityGroupRuleCreateErr wraps err, from the create POST op
// just sent, with a hint to list the group's rules before creating again,
// unless err is already a 4xx *core.APIError: a 4xx means the server
// rejected the request outright, so nothing was created and the exact same
// call is safe to retry. Any other error leaves whether the rule was
// created unknown.
func wrapAmbiguousSecurityGroupRuleCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list the group's rules before creating it again: %w", op, err)
}

// DeleteSecurityGroupRuleInput identifies the rule to delete, within the
// group it must belong to.
type DeleteSecurityGroupRuleInput struct {
	SecurityGroupID     string `vngcloud:"required"`
	SecurityGroupRuleID string `vngcloud:"required"`
}

type DeleteSecurityGroupRuleOutput struct{}

// DeleteSecurityGroupRule deletes a rule from a security group. It lists
// the group's rules first with ListSecurityGroupRules and returns NotFound,
// sending nothing, when no rule in that list has the given
// SecurityGroupRuleID: some servers accept any group id in this path and
// silently ignore it, so without this check a wrong SecurityGroupID could
// delete a rule that belongs to a different group.
//
// DELETE is idempotent and keeps the transport's normal retries; a retry
// that finds the rule already gone returns NotFound.
func (c *Client) DeleteSecurityGroupRule(ctx context.Context, in *DeleteSecurityGroupRuleInput) (*DeleteSecurityGroupRuleOutput, error) {
	const op = "network.DeleteSecurityGroupRule"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SecurityGroupID", in.SecurityGroupID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SecurityGroupRuleID", in.SecurityGroupRuleID); err != nil {
		return nil, err
	}

	rules, err := c.ListSecurityGroupRules(ctx, &ListSecurityGroupRulesInput{SecurityGroupID: in.SecurityGroupID})
	if err != nil {
		return nil, err
	}
	found := false
	for _, rule := range rules.Items {
		if rule.ID == in.SecurityGroupRuleID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: %s: security group rule %s not found in security group %s", core.ErrNotFound, op, in.SecurityGroupRuleID, in.SecurityGroupID)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "secgroups", in.SecurityGroupID, "secgroupRules", in.SecurityGroupRuleID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteSecurityGroupRuleOutput{}, nil
}
