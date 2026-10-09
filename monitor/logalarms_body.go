package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
)

// LogAlarmSeverityLow, LogAlarmSeverityMedium, and LogAlarmSeverityHigh name
// a log alarm's Severity. It is a plain string field, so a new value the
// console adds later reaches the server unchanged.
const (
	LogAlarmSeverityLow    = "LOW"
	LogAlarmSeverityMedium = "MEDIUM"
	LogAlarmSeverityHigh   = "HIGH"
)

// LogAlarmThresholdTypeFrequency, LogAlarmThresholdTypeFlatline, and
// LogAlarmThresholdTypeMetricAggregation name a log alarm's ThresholdType.
const (
	LogAlarmThresholdTypeFrequency         = "frequency"
	LogAlarmThresholdTypeFlatline          = "flatline"
	LogAlarmThresholdTypeMetricAggregation = "metric_aggregation"
)

// LogAlarmConditionGT, LogAlarmConditionGTE, LogAlarmConditionLT, and
// LogAlarmConditionLTE name a log alarm's Condition.
const (
	LogAlarmConditionGT  = "gt"
	LogAlarmConditionGTE = "gte"
	LogAlarmConditionLT  = "lt"
	LogAlarmConditionLTE = "lte"
)

// LogAlarmStatusCreating and LogAlarmStatusUpdating are the two Alarm.Status
// values CreateLogAlarm and UpdateLogAlarm's own wait treats as unsettled;
// any other status, including empty, counts as settled.
const (
	LogAlarmStatusCreating = "CREATING"
	LogAlarmStatusUpdating = "UPDATING"
)

// logAlarmPollInterval and logAlarmWaitBound are CreateLogAlarm and
// UpdateLogAlarm's own wait cadence and bound.
const (
	logAlarmPollInterval = 2 * time.Second
	logAlarmWaitBound    = 60 * time.Second
)

// logAlarmDefaultResendPeriod is the console's own default resendPeriod
// for a nil CreateLogAlarmInput.Resend.
const logAlarmDefaultResendPeriod = 30

// matchAllLogFilter is the filter body CreateLogAlarm sends when QueryString
// and Filter are both empty: four empty lists, which the console's own query
// parser produces for no query at all.
const matchAllLogFilter = `{"type":"bool","value":{"filter":[],"should":[],"must":[],"mustNot":[]}}`

// consoleMatchAllLogFilter returns the filter the console's edit page builds
// for a plain-text query: a phrase match on query. An empty query has no
// phrase to match, so it gets matchAllLogFilter.
func consoleMatchAllLogFilter(query string) json.RawMessage {
	if query == "" {
		return json.RawMessage(matchAllLogFilter)
	}
	q, err := json.Marshal(query)
	if err != nil {
		return json.RawMessage(matchAllLogFilter)
	}
	return json.RawMessage(`{"type":"bool","value":{"filter":[],"should":[],"must":[{"type":"bool","value":{"filter":[],"must":[{"type":"multi_match","value":{"type":"phrase","query":` + string(q) + `,"lenient":true}}],"should":[],"mustNot":[]}}],"mustNot":[]}}`)
}

// logAlarmFields holds a log alarm's fields already resolved to their final
// values: CreateLogAlarm's defaults, or UpdateLogAlarm's merge of the read
// with the caller's set fields. buildLogAlarmBody turns it into the wire body
// both writes send (ADR 0002 rule 8).
type logAlarmFields struct {
	// Update marks an UpdateLogAlarm body, which also carries the log
	// detail's own ID and the logProject and logProjectName keys the console
	// sends.
	Update bool
	ID     string
	// SendResendDetail makes the body carry resendEnabled, resendPeriod, and
	// resendTimes. The console's update omits them; resendStatus is always sent.
	SendResendDetail bool
	Name             string
	Description      string
	Severity         string
	LogProjectID     string
	ProjectName      string
	QueryString      string
	LogSearchQuery   string
	Filter           json.RawMessage
	ThresholdType    string
	Condition        string
	ThresholdValue   float64
	TimeFrame        int
	GroupByField     string
	AggField         string
	AggType          string
	InAlarm          []string
	OK               []string
	Resend           LogAlarmResend
}

// logAlarmBody is CreateLogAlarm and UpdateLogAlarm's shared request body.
// GroupByField is a pointer so an empty value marshals as JSON null rather
// than being omitted, per the design's create body: one field name or
// null, never absent. AggField and AggType omit their keys entirely when
// empty, matching the design's "sent only when set". Filter omits its key
// when nil, so UpdateLogAlarm can resend a read that had no filter as
// having none, rather than an explicit null.
type logAlarmBody struct {
	ID             string          `json:"id,omitempty"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Severity       string          `json:"severity"`
	LogProject     string          `json:"logProject,omitempty"`
	LogProjectName string          `json:"logProjectName,omitempty"`
	LogProjectID   string          `json:"logProjectId"`
	ProjectName    string          `json:"projectName"`
	Zone           string          `json:"zone"`
	QueryString    string          `json:"queryString"`
	LogSearchQuery string          `json:"logSearchQuery"`
	Filter         json.RawMessage `json:"filter,omitempty"`
	ThresholdType  string          `json:"thresholdType"`
	Condition      string          `json:"condition"`
	ThresholdValue float64         `json:"thresholdValue"`
	TimeFrame      int             `json:"timeFrame"`
	GroupByField   *string         `json:"groupByField"`
	AggField       string          `json:"metricAggKey,omitempty"`
	AggType        string          `json:"metricAggType,omitempty"`
	Reason         string          `json:"reason"`
	InAlarm        string          `json:"inAlarm"`
	OK             string          `json:"ok"`
	Undetermined   string          `json:"undetermined"`
	ResendEnabled  *bool           `json:"resendEnabled,omitempty"`
	ResendPeriod   *int            `json:"resendPeriod,omitempty"`
	ResendTimes    *int            `json:"resendTimes,omitempty"`
	ResendStatus   string          `json:"resendStatus"`
}

// logAlarmConditionOp names <op> in the design's reason format, keyed by
// Condition. gte and lte carry the design's own leading and trailing
// spaces; gt and lt carry none. Both are the console template's own
// values, reproduced as the design's source records them.
var logAlarmConditionOp = map[string]string{
	LogAlarmConditionGT:  ">",
	LogAlarmConditionGTE: " >= ",
	LogAlarmConditionLT:  "<",
	LogAlarmConditionLTE: " <= ",
}

// buildLogAlarmReason builds the reason field's display text: "query(<q>)
// <op><value>" for every threshold type but metric_aggregation, which
// instead leads with "<aggType>[<aggField>](<q>)". value is formatted the
// way JSON's own number-to-string conversion would, without a trailing
// ".0" for a whole number.
func buildLogAlarmReason(f logAlarmFields, queryString string) string {
	op := logAlarmConditionOp[f.Condition]
	value := strconv.FormatFloat(f.ThresholdValue, 'f', -1, 64)
	if f.ThresholdType == LogAlarmThresholdTypeMetricAggregation {
		return fmt.Sprintf("%s[%s](%s) %s%s", f.AggType, f.AggField, queryString, op, value)
	}
	return fmt.Sprintf("query(%s) %s%s", queryString, op, value)
}

// joinChannelIDs encodes ids in the design's create-body format for
// inAlarm and ok: each ID followed by a comma, including the last one, the
// same format splitChannelIDs reads back. An empty ids returns "".
func joinChannelIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(id)
		b.WriteByte(',')
	}
	return b.String()
}

// buildLogAlarmBody returns f's resolved values unchanged in the request
// body. CreateLogAlarm resolves its match-all defaults before calling this
// builder. UpdateLogAlarm must preserve every unset read field exactly.
func buildLogAlarmBody(f logAlarmFields) logAlarmBody {
	var groupByField *string
	if f.GroupByField != "" {
		gbf := f.GroupByField
		groupByField = &gbf
	}

	body := logAlarmBody{
		ID:             f.ID,
		Name:           f.Name,
		Description:    f.Description,
		Severity:       f.Severity,
		LogProjectID:   f.LogProjectID,
		ProjectName:    f.ProjectName,
		Zone:           "",
		QueryString:    f.QueryString,
		LogSearchQuery: f.LogSearchQuery,
		Filter:         f.Filter,
		ThresholdType:  f.ThresholdType,
		Condition:      f.Condition,
		ThresholdValue: f.ThresholdValue,
		TimeFrame:      f.TimeFrame,
		GroupByField:   groupByField,
		AggField:       f.AggField,
		AggType:        f.AggType,
		Reason:         buildLogAlarmReason(f, f.QueryString),
		InAlarm:        joinChannelIDs(f.InAlarm),
		OK:             joinChannelIDs(f.OK),
		Undetermined:   "",
		ResendStatus:   strings.Join(f.Resend.Statuses, ","),
	}
	if f.Update {
		body.LogProject = f.LogProjectID
		body.LogProjectName = f.ProjectName
	}
	if f.SendResendDetail {
		body.ResendEnabled = &f.Resend.Enabled
		body.ResendPeriod = &f.Resend.Period
		body.ResendTimes = &f.Resend.Times
	}
	return body
}

// checkLogAlarmChannelIDs runs core.CheckPathID on every id in ids, naming
// field in any error. A comma in an id would otherwise reach the joined
// inAlarm or ok string as an extra, unintended channel ID; CheckPathID's
// pattern excludes it along with every other path-unsafe character.
func checkLogAlarmChannelIDs(op, field string, ids []string) error {
	for _, id := range ids {
		if err := core.CheckPathID(op, field, id); err != nil {
			return err
		}
	}
	return nil
}

// isJSONObject reports whether data decodes as a JSON object.
func isJSONObject(data json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}

// checkLogAlarmQueryFilterPairing refuses a queryString and filter that are
// not both empty or both set, and a set filter that is not a JSON object.
func checkLogAlarmQueryFilterPairing(op, queryString string, filter json.RawMessage) error {
	if (queryString == "") != (len(filter) == 0) {
		return fmt.Errorf("%w: %s: QueryString and Filter must both be set or both left empty", core.ErrInvalidInput, op)
	}
	if len(filter) > 0 && !isJSONObject(filter) {
		return fmt.Errorf("%w: %s: Filter must be a JSON object", core.ErrInvalidInput, op)
	}
	return nil
}

// checkLogAlarmThresholdValue refuses a NaN or infinite value, before any
// request: the create body's reason text and the console's own threshold
// comparison make no sense for either, the same guard CreateLogProject
// runs on MaxPrice before ordering.
func checkLogAlarmThresholdValue(op string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%w: %s: ThresholdValue must be a finite number, got %v", core.ErrInvalidInput, op, value)
	}
	return nil
}

// checkLogAlarmUpdatable refuses UpdateLogAlarm's full-replace PUT when
// current cannot supply every field the create body always sends:
// current.Log nil (no alarmLog and no top-level inAlarm/ok at all in the
// read), or a read missing a create-body field. Sending the PUT anyway
// would replace those fields with the wire's own zero values instead of
// leaving them alone.
func checkLogAlarmUpdatable(op, alarmID string, current *Alarm) error {
	if current.Log == nil {
		return fmt.Errorf("%w: %s: alarm %s's read carries no log alarm detail to update from", core.ErrInvalidInput, op, alarmID)
	}
	d := current.Log
	if current.Name == "" || current.Severity == "" || d.LogProjectID == "" || d.LogProjectName == "" || d.ThresholdType == "" || d.Condition == "" || d.TimeFrame == 0 {
		return fmt.Errorf("%w: %s: alarm %s's read is missing a field the create body always sends; refusing a full-replace update", core.ErrInvalidInput, op, alarmID)
	}
	return nil
}

// defaultLogAlarmCondition returns condition lowercased when set (the API
// accepts only lowercase on a write, though a read returns it uppercase), else
// LogAlarmConditionLT for a flatline threshold or LogAlarmConditionGT for
// every other threshold type, per the create body's own default.
func defaultLogAlarmCondition(condition, thresholdType string) string {
	if condition != "" {
		return strings.ToLower(condition)
	}
	if thresholdType == LogAlarmThresholdTypeFlatline {
		return LogAlarmConditionLT
	}
	return LogAlarmConditionGT
}

// resolveLogAlarmResend returns *r, or the console's own defaults
// (disabled, resend on ALARM only, every 30 minutes, no limit) when r is
// nil.
func resolveLogAlarmResend(r *LogAlarmResend) LogAlarmResend {
	if r == nil {
		return LogAlarmResend{Statuses: []string{"ALARM"}, Period: logAlarmDefaultResendPeriod}
	}
	return *r
}

// maxLogAlarmListPages bounds findLogAlarmByName's page walk, the same way
// maxGetChannelPages bounds GetChannel's: a server that never returns an
// empty page and never reports a TotalItem the walk can reach would
// otherwise turn a single call into an infinite loop.
const maxLogAlarmListPages = 1000

// findLogAlarmByName returns the log alarm named exactly name, scanning
// every page of a Name-filtered ListAlarms read for an exact match, rather
// than trusting that filter to return only exact matches: its matching
// behavior (exact vs. substring) is unconfirmed, the same assumption
// findLogProjectByName makes for log projects.
func (c *Client) findLogAlarmByName(ctx context.Context, op, name string) (*Alarm, error) {
	seen := 0
	for page := core.DefaultPage; page <= maxLogAlarmListPages; page++ {
		list, err := c.listAlarms(ctx, op, &ListAlarmsInput{Kind: AlarmKindLog, Name: name, Page: page, Size: core.DefaultPageSize})
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			if list.Items[i].Name == name {
				return &list.Items[i], nil
			}
		}
		seen += len(list.Items)
		if len(list.Items) == 0 || seen >= list.TotalItem {
			break
		}
	}
	return nil, nil
}

// refuseIfLogAlarmNameExists refuses to create a log alarm named name when
// one already exists on the account, before any other request: when the
// create response carries no id, the wait below settles by this same exact
// name, so a duplicate name risks settling on the existing alarm instead of
// the one this call is about to create.
func (c *Client) refuseIfLogAlarmNameExists(ctx context.Context, op, name string) error {
	existing, err := c.findLogAlarmByName(ctx, op, name)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("%w: %s: a log alarm named %q already exists", core.ErrInvalidInput, op, name)
	}
	return nil
}

// logAlarmSettled reports whether status counts as settled: neither
// LogAlarmStatusCreating nor LogAlarmStatusUpdating, including an empty
// status.
func logAlarmSettled(status string) bool {
	return status != LogAlarmStatusCreating && status != LogAlarmStatusUpdating
}

// waitLogAlarmByID polls GetAlarm(id) every logAlarmPollInterval, up to
// logAlarmWaitBound, until its Status settles. Any error, including the
// bound running out, is wrapped in dns.ErrNotSettled unless it already is
// one: once id is known to exist, any further uncertainty here means the
// write must not be sent again.
func (c *Client) waitLogAlarmByID(ctx context.Context, op, id string) (*Alarm, error) {
	var found *Alarm
	err := poll(ctx, c.now, c.sleep, logAlarmPollInterval, logAlarmWaitBound,
		func(ctx context.Context) (bool, error) {
			alarm, err := c.getAlarm(ctx, op, id)
			if err != nil {
				return true, err
			}
			found = alarm
			return logAlarmSettled(alarm.Status), nil
		},
		func() error {
			return fmt.Errorf("%w: %s: log alarm %s was accepted; do not send the same write again", dns.ErrNotSettled, op, id)
		},
	)
	if err != nil && !errors.Is(err, dns.ErrNotSettled) {
		err = fmt.Errorf("%w: %s: log alarm %s: %w", dns.ErrNotSettled, op, id, err)
	}
	return found, err
}

// waitLogAlarmByName is CreateLogAlarm's own wait when the create response
// carried no id: it polls the log alarm list by exact name, the same way
// waitLogProjectActive polls log projects by name, until a match's Status
// settles.
func (c *Client) waitLogAlarmByName(ctx context.Context, op, name string) (*Alarm, error) {
	var found *Alarm
	err := poll(ctx, c.now, c.sleep, logAlarmPollInterval, logAlarmWaitBound,
		func(ctx context.Context) (bool, error) {
			alarm, err := c.findLogAlarmByName(ctx, op, name)
			if err != nil {
				return true, err
			}
			if alarm == nil {
				return false, nil
			}
			found = alarm
			return logAlarmSettled(alarm.Status), nil
		},
		func() error {
			return fmt.Errorf("%w: %s: log alarm %q was accepted; do not send the same write again", dns.ErrNotSettled, op, name)
		},
	)
	if err != nil && !errors.Is(err, dns.ErrNotSettled) {
		err = fmt.Errorf("%w: %s: log alarm %q: %w", dns.ErrNotSettled, op, name, err)
	}
	return found, err
}

// logAlarmWriteResponse decodes a write's id from either data.id or a
// top-level id, whichever the response carries: the console ignores every
// create, update, and delete response, so their shapes are unseen.
// UnmarshalJSON never returns an error: a shape with no usable id, such as
// {"data":"success"} or {"data":true}, leaves ID empty rather than failing
// the create, which falls back to its own wait by name.
type logAlarmWriteResponse struct {
	ID string
}

func (r *logAlarmWriteResponse) UnmarshalJSON(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil //nolint:nilerr // a shape this type cannot read carries no id; see the type's own doc comment
	}
	if id := decodeLogAlarmWriteID(top["id"]); id != "" {
		r.ID = id
		return nil
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(top["data"], &nested); err == nil {
		r.ID = decodeLogAlarmWriteID(nested["id"])
	}
	return nil
}

// decodeLogAlarmWriteID decodes raw as a flexibleString, returning "" for
// a missing, nil, or non-string, non-numeric raw.
func decodeLogAlarmWriteID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var id flexibleString
	if err := json.Unmarshal(raw, &id); err != nil {
		return ""
	}
	return string(id)
}

// wrapAmbiguousLogAlarmCreateErr wraps err, from the create POST just sent,
// with a hint to list log alarms by name before creating again, unless err
// is already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so nothing was created and the exact same call is safe
// to retry. Any other error, a 5xx or a failure before any response ever
// came back, leaves whether the alarm was created unknown.
func wrapAmbiguousLogAlarmCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list log alarms by name before creating again: %w", op, err)
}
