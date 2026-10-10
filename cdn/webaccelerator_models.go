package cdn

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Web Accelerator status values.
const (
	StatusDisabled  = 0
	StatusActive    = 1
	StatusDeploying = 3
	StatusDeleting  = 4
	StatusDisabling = 5
)

// StatusName returns the name of a Web Accelerator status: DISABLED, ACTIVE,
// DEPLOYING, DELETING, or DISABLING. Any other value gives UNKNOWN(<n>).
func StatusName(status int) string {
	switch status {
	case StatusDisabled:
		return "DISABLED"
	case StatusActive:
		return "ACTIVE"
	case StatusDeploying:
		return "DEPLOYING"
	case StatusDeleting:
		return "DELETING"
	case StatusDisabling:
		return "DISABLING"
	}
	return fmt.Sprintf("UNKNOWN(%d)", status)
}

// flexID decodes an ID the server sends as a JSON string or integer.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return err
		}
		*f = flexID(text)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("cdn: an ID must be a JSON string or integer")
	}
	*f = flexID(n.String())
	return nil
}

// WebAcceleratorSummary is one item of ListWebAccelerators. The list fills
// only these fields of a CDN; GetWebAccelerator holds the rest.
type WebAcceleratorSummary struct {
	CDNID      string   `json:"cdnId"`
	DomainName string   `json:"domainName"`
	CDNDomain  string   `json:"cdnDomain"`
	CNames     []string `json:"cName"`
	Status     int      `json:"status"`
	StatusName string   `json:"statusName"`
}

func (s *WebAcceleratorSummary) UnmarshalJSON(b []byte) error {
	type plain WebAcceleratorSummary
	aux := struct {
		*plain
		CDNID flexID `json:"cdnId"`
	}{plain: (*plain)(s)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	s.CDNID = string(aux.CDNID)
	s.StatusName = StatusName(s.Status)
	return nil
}

// WebAccelerator is one Web Accelerator CDN. CDNDomain is the generated name
// the customer's DNS points at. CertificateID is "default" for the shared
// certificate. The model has no field for the account fields the server
// sends. PageRules and AdvancedRule stay the server's JSON: no call writes
// them, and an update sends them back unchanged.
type WebAccelerator struct {
	CDNID              string          `json:"cdnId"`
	Type               string          `json:"type"`
	DomainName         string          `json:"domainName"`
	CDNDomain          string          `json:"cdnDomain"`
	CNames             []string        `json:"cName"`
	Status             int             `json:"status"`
	StatusName         string          `json:"statusName"`
	CertificateID      string          `json:"sslId"`
	LBType             string          `json:"lbType"`
	OriginHostHeader   string          `json:"originHostHeader"`
	FailOverErrorCodes []string        `json:"failOverErrorCode"`
	UseSSL             bool            `json:"useSsl"`
	UseSmallFile       bool            `json:"useSmallFile"`
	EnableGzip         bool            `json:"enableGzip"`
	Upstreams          []Upstream      `json:"upstreams"`
	DefaultRuleActions []RuleAction    `json:"defaultRuleAction"`
	PageRules          json.RawMessage `json:"childrenRule"`
	AdvancedRule       json.RawMessage `json:"childrenAdvanceRule"`
}

func (w *WebAccelerator) UnmarshalJSON(b []byte) error {
	type plain WebAccelerator
	aux := struct {
		*plain
		CDNID flexID `json:"cdnId"`
	}{plain: (*plain)(w)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	w.CDNID = string(aux.CDNID)
	w.StatusName = StatusName(w.Status)
	return nil
}

// Upstream is one origin of a CDN. The detail read has no upstream type,
// origin value, or per-origin SSL flag.
type Upstream struct {
	ID        string `json:"cdnUpstreamId"`
	Priority  int    `json:"priority"`
	IPAddress string `json:"ipaddress"`
	Status    int    `json:"status"`
}

func (u *Upstream) UnmarshalJSON(b []byte) error {
	type plain Upstream
	aux := struct {
		*plain
		ID flexID `json:"cdnUpstreamId"`
	}{plain: (*plain)(u)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	u.ID = string(aux.ID)
	return nil
}

// RuleAction is one default rule action of a CDN. Value stays the server's
// string: for hsts it is JSON object text, and for minify JSON array text.
type RuleAction struct {
	ID    string `json:"id"`
	Name  string `json:"actionName"`
	Value string `json:"value"`
	Order int    `json:"order"`
}

func (a *RuleAction) UnmarshalJSON(b []byte) error {
	type plain RuleAction
	aux := struct {
		*plain
		ID flexID `json:"id"`
	}{plain: (*plain)(a)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	a.ID = string(aux.ID)
	return nil
}
