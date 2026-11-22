package loadbalancer

import "slices"

func submittedString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func submittedListener(listener Listener, body listenerWriteBody) Listener {
	listener.DefaultPoolID = body.DefaultPoolID
	listener.TimeoutClient, listener.TimeoutMember, listener.TimeoutConnection = body.TimeoutClient, body.TimeoutMember, body.TimeoutConnection
	listener.AllowedCIDRs = body.AllowedCIDRs
	listener.CertificateAuthorities = slices.Clone(body.CertificateAuthorities)
	listener.DefaultCertificateAuthority = submittedString(body.DefaultCertificateAuthority)
	listener.ClientCertificateAuthentication = submittedString(body.ClientCertificate)
	listener.InsertHeaders = slices.Clone(body.InsertHeaders)
	return listener
}

func submittedPool(pool Pool, body createPoolBody, protocol string, monitor *HealthMonitor) Pool {
	pool.LoadBalanceMethod = body.Algorithm
	if body.Stickiness != nil {
		pool.Stickiness = *body.Stickiness
	}
	if body.TLSEncryption != nil {
		pool.TLSEncryption = *body.TLSEncryption
	}
	hm := HealthMonitor{}
	if monitor != nil {
		hm = *monitor
	}
	hm.HealthCheckProtocol = protocol
	hm.HealthyThreshold, hm.UnhealthyThreshold = body.HealthMonitor.HealthyThreshold, body.HealthMonitor.UnhealthyThreshold
	hm.Interval, hm.Timeout = body.HealthMonitor.Interval, body.HealthMonitor.Timeout
	hm.HealthCheckPath = submittedString(body.HealthMonitor.HealthCheckPath)
	hm.HealthCheckMethod = submittedString(body.HealthMonitor.HealthCheckMethod)
	hm.HTTPVersion = submittedString(body.HealthMonitor.HTTPVersion)
	hm.DomainName = submittedString(body.HealthMonitor.DomainName)
	hm.SuccessCode = submittedString(body.HealthMonitor.SuccessCode)
	pool.HealthMonitor = &hm
	return pool
}

func submittedPolicy(policy Policy, body policyWriteBody, replaceRules bool) Policy {
	policy.Action, policy.RedirectPoolID, policy.RedirectURL = body.Action, body.RedirectPoolID, body.RedirectURL
	policy.RedirectHTTPCode = body.RedirectHTTPCode
	policy.KeepQueryString = body.KeepQueryString != nil && *body.KeepQueryString
	if replaceRules {
		policy.L7Rules = make([]L7Rule, len(body.Rules))
		for i, rule := range body.Rules {
			policy.L7Rules[i] = L7Rule{RuleType: rule.RuleType, CompareType: rule.CompareType, RuleValue: rule.RuleValue}
		}
	}
	return policy
}
