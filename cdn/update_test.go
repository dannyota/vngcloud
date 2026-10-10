package cdn

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

func ptr[T any](v T) *T { return &v }

// updateBody runs an update on an ACTIVE sim and returns the PUT body.
func updateBody(t *testing.T, in UpdateWebAcceleratorInput, mutate func(s *sim)) (map[string]json.RawMessage, *sim, error) {
	t.Helper()
	s := newSim(t, StatusActive, StatusDeploying, StatusActive)
	if mutate != nil {
		mutate(s)
	}
	h, _ := s.harness(t)
	in.CDNID = cdnID
	_, err := h.UpdateWebAccelerator(context.Background(), &in)
	raw := s.log.updateBodyText()
	var body map[string]json.RawMessage
	if raw != "" {
		if jerr := json.Unmarshal([]byte(raw), &body); jerr != nil {
			t.Fatalf("body is not JSON: %v", jerr)
		}
	}
	return body, s, err
}

type wireAction struct {
	ID    json.RawMessage `json:"id"`
	Name  string          `json:"actionName"`
	Value string          `json:"value"`
	Order int             `json:"order"`
}

func actionsOf(t *testing.T, body map[string]json.RawMessage) []wireAction {
	t.Helper()
	var out []wireAction
	if err := json.Unmarshal(body["defaultRuleAction"], &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func originalBody(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(readFixture(t, "webaccelerator-detail.json")), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data
}

func TestUpdateChangesOneActionKeepingIDAndEverythingElse(t *testing.T) {
	body, s, err := updateBody(t, UpdateWebAcceleratorInput{
		SetRuleActions: []RuleActionInput{{Name: "browserCache", Value: "1d"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	orig := originalBody(t)
	got, was := actionsOf(t, body), actionsOf(t, orig)
	if len(got) != len(was) {
		t.Fatalf("actions = %d, want %d", len(got), len(was))
	}
	for i := range got {
		if got[i].Name == "browserCache" {
			if got[i].Value != "1d" {
				t.Errorf("browserCache = %q", got[i].Value)
			}
			got[i].Value = was[i].Value
		}
		if !reflect.DeepEqual(got[i], was[i]) {
			t.Errorf("action %d = %+v, want %+v", i, got[i], was[i])
		}
	}
	for key, val := range orig {
		if key == "defaultRuleAction" {
			continue
		}
		if string(body[key]) != compact(t, val) {
			t.Errorf("field %q = %s, want it sent as read: %s", key, body[key], val)
		}
	}
	if _, ok := body["userUuid"]; !ok {
		t.Error("userUuid not sent back")
	}
	if s.log.count("PUT cdn/update") != 1 {
		t.Errorf("updates = %d", s.log.count("PUT cdn/update"))
	}
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateNewActionHasNoID(t *testing.T) {
	body, _, err := updateBody(t, UpdateWebAcceleratorInput{
		SetRuleActions: []RuleActionInput{{Name: "newThing", Value: "on"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	acts := actionsOf(t, body)
	last := acts[len(acts)-1]
	if last.Name != "newThing" || last.Value != "on" || last.ID != nil {
		t.Fatalf("last = %+v, want a new action with no id", last)
	}
	if len(acts) != len(actionsOf(t, originalBody(t)))+1 {
		t.Fatalf("actions = %d", len(acts))
	}
}

func TestUpdateRemovedActionIsAbsentAndUnknownIsIgnored(t *testing.T) {
	body, _, err := updateBody(t, UpdateWebAcceleratorInput{
		RemoveRuleActions: []string{"nosniff", "noSuchAction"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	acts := actionsOf(t, body)
	if len(acts) != len(actionsOf(t, originalBody(t)))-1 {
		t.Fatalf("actions = %d", len(acts))
	}
	for _, a := range acts {
		if a.Name == "nosniff" {
			t.Fatal("nosniff still sent")
		}
	}
}

// The server gives alwaysHttps a new id on every update, so a later update
// must send the id it reads, never an earlier one.
func TestUpdateUsesTheFreshAlwaysHTTPSID(t *testing.T) {
	var fresh string
	s := newSim(t, StatusActive, StatusDeploying, StatusActive)
	s.detailOverride = func(n int) (string, bool) {
		if n > 1 {
			return "", false
		}
		body := detailWithStatus(t, StatusActive)
		fresh = "action-fresh-" + strings.Repeat("x", 3)
		return strings.Replace(body, `"id":"action-12"`, `"id":"`+fresh+`"`, 1), true
	}
	h, _ := s.harness(t)
	if _, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
		CDNID: cdnID, SetRuleActions: []RuleActionInput{{Name: "alwaysHttps", Value: "on"}},
	}); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s.log.updateBodyText()), &body); err != nil {
		t.Fatal(err)
	}
	for _, a := range actionsOf(t, body) {
		if a.Name == "alwaysHttps" && (string(a.ID) != `"`+fresh+`"` || a.Value != "on") {
			t.Fatalf("alwaysHttps = %+v, want the fresh id %q", a, fresh)
		}
	}
}

func TestUpdateSendsIDsInTheFormTheyWereRead(t *testing.T) {
	s := newSim(t, StatusActive, StatusDeploying, StatusActive)
	s.detailOverride = func(n int) (string, bool) {
		if n > 1 {
			return "", false
		}
		body := detailWithStatus(t, StatusActive)
		body = strings.Replace(body, `"id":"action-1"`, `"id":77`, 1)
		body = strings.Replace(body, `"cdnUpstreamId":"upstream-1"`, `"cdnUpstreamId":55`, 1)
		return body, true
	}
	h, _ := s.harness(t)
	if _, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{
		CDNID:          cdnID,
		SetRuleActions: []RuleActionInput{{Name: "nosniff", Value: "off"}},
		Upstreams:      []UpstreamInput{{ID: "55", Priority: 5, IPAddress: "198.51.100.7"}},
	}); err != nil {
		t.Fatal(err)
	}
	sent := s.log.updateBodyText()
	if !strings.Contains(sent, `"id":77`) || !strings.Contains(sent, `"cdnUpstreamId":55`) {
		t.Fatalf("IDs not sent back as integers: %s", sent)
	}
}

func TestDecodeAcceptsStringAndIntegerIDs(t *testing.T) {
	var wa WebAccelerator
	in := `{"cdnId":12,"status":1,"upstreams":[{"cdnUpstreamId":3,"priority":1}],"defaultRuleAction":[{"id":4,"actionName":"a","value":"b"},{"id":"x-5","actionName":"c","value":null}]}`
	if err := json.Unmarshal([]byte(in), &wa); err != nil {
		t.Fatal(err)
	}
	if wa.CDNID != "12" || wa.Upstreams[0].ID != "3" || wa.DefaultRuleActions[0].ID != "4" || wa.DefaultRuleActions[1].ID != "x-5" || wa.DefaultRuleActions[1].Value != "" {
		t.Fatalf("wa = %+v", wa)
	}
	for _, bad := range []string{`{"cdnId":true}`, `{"cdnId":{}}`, `{"upstreams":[{"cdnUpstreamId":[1]}]}`} {
		if err := json.Unmarshal([]byte(bad), &wa); err == nil {
			t.Errorf("%s decoded", bad)
		}
	}
}

func TestUpdateScalarsAndLists(t *testing.T) {
	body, _, err := updateBody(t, UpdateWebAcceleratorInput{
		LBType: ptr("ip_hash"), CertificateID: ptr("cert-9"), OriginHostHeader: ptr("origin.example.test"),
		FailOverErrorCodes: []string{"502"}, CNames: []string{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"lbType": `"ip_hash"`, "sslId": `"cert-9"`, "originHostHeader": `"origin.example.test"`,
		"failOverErrorCode": `["502"]`, "cName": `[]`,
	} {
		if string(body[key]) != want {
			t.Errorf("%s = %s, want %s", key, body[key], want)
		}
	}
}

func TestUpdateNilKeepsAndEmptyClears(t *testing.T) {
	// FailOverErrorCodes nil keeps the four codes; CNames empty clears.
	body, _, err := updateBody(t, UpdateWebAcceleratorInput{CNames: []string{}}, func(s *sim) {
		s.detailOverride = func(n int) (string, bool) {
			if n > 1 {
				return "", false
			}
			return strings.Replace(detailWithStatus(t, StatusActive), `"cName":[]`, `"cName":["old.example.test"]`, 1), true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body["cName"]) != `[]` {
		t.Errorf("cName = %s, want it cleared", body["cName"])
	}
	if string(body["failOverErrorCode"]) != `["500","502","503","504"]` {
		t.Errorf("failOverErrorCode = %s, want it kept", body["failOverErrorCode"])
	}
}

func TestUpdateUpstreams(t *testing.T) {
	t.Run("nil keeps the read list", func(t *testing.T) {
		body, _, err := updateBody(t, UpdateWebAcceleratorInput{LBType: ptr("ip_hash")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(body["upstreams"]) != compact(t, originalBody(t)["upstreams"]) {
			t.Errorf("upstreams = %s", body["upstreams"])
		}
	})
	t.Run("edit by ID sends only that entry", func(t *testing.T) {
		body, _, err := updateBody(t, UpdateWebAcceleratorInput{
			Upstreams: []UpstreamInput{{ID: "upstream-1", Priority: 20, IPAddress: "198.51.100.9", UseSSL: true}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ups []map[string]json.RawMessage
		if err := json.Unmarshal(body["upstreams"], &ups); err != nil || len(ups) != 1 {
			t.Fatalf("upstreams = %s", body["upstreams"])
		}
		u := ups[0]
		for key, want := range map[string]string{
			"cdnUpstreamId": `"upstream-1"`, "priority": `20`, "ipaddress": `"198.51.100.9"`,
			"upstreamType": `"httpOrigin"`, "originValue": `null`, "useSsl": `true`,
		} {
			if string(u[key]) != want {
				t.Errorf("%s = %s, want %s", key, u[key], want)
			}
		}
	})
	t.Run("add has no ID", func(t *testing.T) {
		body, _, err := updateBody(t, UpdateWebAcceleratorInput{
			Upstreams: []UpstreamInput{{Priority: 1, IPAddress: "198.51.100.9", UpstreamType: "s3Origin", OriginValue: ptr("bucket")}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ups []map[string]json.RawMessage
		_ = json.Unmarshal(body["upstreams"], &ups)
		if len(ups) != 1 || ups[0]["cdnUpstreamId"] != nil || string(ups[0]["upstreamType"]) != `"s3Origin"` || string(ups[0]["originValue"]) != `"bucket"` {
			t.Fatalf("upstreams = %s", body["upstreams"])
		}
	})
	t.Run("unknown ID sends nothing", func(t *testing.T) {
		_, s, err := updateBody(t, UpdateWebAcceleratorInput{
			Upstreams: []UpstreamInput{{ID: "nope", IPAddress: "198.51.100.9"}},
		}, nil)
		if !errors.Is(err, vngcloud.ErrInvalidInput) || s.log.count("PUT") != 0 {
			t.Fatalf("err = %v, puts = %d", err, s.log.count("PUT"))
		}
	})
}

func TestUpdateWithNoChangeSendsNothing(t *testing.T) {
	for name, in := range map[string]UpdateWebAcceleratorInput{
		"same value":       {SetRuleActions: []RuleActionInput{{Name: "browserCache", Value: "1M"}}},
		"same scalar":      {LBType: ptr("rr")},
		"same list":        {FailOverErrorCodes: []string{"500", "502", "503", "504"}},
		"remove unknown":   {RemoveRuleActions: []string{"noSuchAction"}},
		"same certificate": {CertificateID: ptr("default")},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSim(t, StatusActive)
			h, _ := s.harness(t)
			in.CDNID = cdnID
			out, err := h.UpdateWebAccelerator(context.Background(), &in)
			if err != nil || out.WebAccelerator.Status != StatusActive {
				t.Fatalf("out = %+v err = %v", out, err)
			}
			if s.log.count("PUT") != 0 || s.log.total() != 1 {
				t.Fatalf("log = %v, want only the read", s.log.lines)
			}
		})
	}
}

func TestUpdateOfDisabledOrBusyCDNSendsNothing(t *testing.T) {
	for status, is := range map[int]error{StatusDisabled: vngcloud.ErrInvalidInput, StatusDeploying: ErrBusy, StatusDisabling: ErrBusy, 9: ErrUnexpectedStatus} {
		s := newSim(t, status)
		h, _ := s.harness(t)
		_, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"a.example.test"}})
		if !errors.Is(err, is) || s.log.count("PUT") != 0 {
			t.Errorf("status %d: err = %v, puts = %d", status, err, s.log.count("PUT"))
		}
		if status == StatusDisabled && !strings.Contains(err.Error(), "enable it first") {
			t.Errorf("message = %q", err)
		}
	}
}

func TestUpdateSendsOnceAfterAServerError(t *testing.T) {
	s := newSim(t, StatusActive)
	s.writeHTTP = http.StatusBadGateway
	h, _ := s.harness(t)
	_, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"a.example.test"}})
	if apiError(t, err).StatusCode != 502 || !strings.Contains(err.Error(), "read the CDN before running it again") {
		t.Fatalf("err = %v", err)
	}
	if s.log.count("PUT cdn/update") != 1 {
		t.Fatalf("updates = %d, want 1", s.log.count("PUT cdn/update"))
	}
}

func TestUpdatePackageRefusalPassesThrough(t *testing.T) {
	s := newSim(t, StatusActive)
	s.writeBody = readFixture(t, "write-package-refused.json")
	h, _ := s.harness(t)
	_, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, SetRuleActions: []RuleActionInput{{Name: "developmentMode", Value: "on"}}})
	apiErr := apiError(t, err)
	if !strings.Contains(apiErr.Message, "please upgrade your package") || apiErr.Code != "500" {
		t.Fatalf("err = %v", err)
	}
	for _, sentinel := range []error{ErrBusy, vngcloud.ErrNotFound, vngcloud.ErrInvalidInput} {
		if errors.Is(err, sentinel) {
			t.Errorf("matches %v", sentinel)
		}
	}
}

// The user UUID the update sends back never reaches an error or the log.
func TestUpdateDoesNotLeakTheUserUUID(t *testing.T) {
	s := newSim(t, StatusActive)
	s.writeBody = `{"success":false,"code":500,"message":"no","data":""}`
	h, _ := s.harness(t)
	out, err := h.UpdateWebAccelerator(context.Background(), &UpdateWebAcceleratorInput{CDNID: cdnID, CNames: []string{"a.example.test"}})
	if err == nil || strings.Contains(err.Error(), "<user-id>") || strings.Contains(h.logs.String(), "<user-id>") {
		t.Fatalf("err = %v", err)
	}
	noLeak(t, out)
}
