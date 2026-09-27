package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// CreateLogAlarmInput creates a log alarm. ThresholdValue is a pointer so
// 0 is a value distinct from leaving it unset; every other numeric or
// string field takes the create body's own default when left at its zero
// value: Severity empty sends LogAlarmSeverityLow, ThresholdType empty
// sends LogAlarmThresholdTypeFrequency, Condition empty sends
// LogAlarmConditionLT for a flatline threshold or LogAlarmConditionGT
// otherwise, TimeFrame 0 sends 5 (minutes), and GroupByField empty sends
// JSON null. QueryString and Filter must both be set or both left empty;
// both empty sends a match-all query. AggField and AggType are sent only
// when set. Resend nil sends the console's own defaults; a non-nil value
// is sent exactly as given, with no range check (ADR 0002 rule 5).
//
// The server checks the name pattern and every value range; the SDK only
// checks that the required fields are set and that IDs match
// core.CheckPathID's pattern (ADR 0002 rule 5).
type CreateLogAlarmInput struct {
	Name           string   `vngcloud:"required"`
	LogProjectID   string   `vngcloud:"required"`
	ThresholdValue *float64 `vngcloud:"required"`

	Description   string
	Severity      string
	QueryString   string
	Filter        json.RawMessage
	ThresholdType string
	Condition     string
	TimeFrame     int
	GroupByField  string
	AggField      string
	AggType       string
	InAlarm       []string
	OK            []string
	Resend        *LogAlarmResend
	NoWait        bool
}

// CreateLogAlarmOutput is CreateLogAlarm's result. AlarmID is empty only
// when NoWait is set and the create response carried no id. Alarm is set
// once the post-create wait finds the alarm settled; NoWait skips that
// wait, leaving Alarm at its zero value.
type CreateLogAlarmOutput struct {
	AlarmID string
	Alarm   Alarm
}

// CreateLogAlarm creates a log alarm on an ACTIVE log project. Before any
// request, it checks LogProjectID and every InAlarm and OK channel ID with
// core.CheckPathID, and the QueryString/Filter pairing rule.
//
// It then lists log alarms by Name and refuses with core.ErrInvalidInput,
// creating nothing, when one already has that exact name (the design's own
// duplicate-name rule, since the wait below can only settle by name when
// the create response carries no id). It reads the log project with
// GetLogProject for its Name, which becomes the body's projectName; a 404
// there returns the SDK's not-found sentinel and creates nothing.
//
// The create is a POST and, like CreateLogProject's order, is never
// retried after a failure that may have already reached the server: after
// any error that is not a 4xx *core.APIError, the alarm may exist, and the
// returned error says to list log alarms by Name before creating again.
//
// Without NoWait, CreateLogAlarm waits up to 60 seconds for the alarm's
// Status to settle (anything but LogAlarmStatusCreating or
// LogAlarmStatusUpdating): by AlarmID when the create response carried one,
// else by listing log alarms for an exact Name match, the same way
// CreateLogProject's own wait works before an id is known. If the bound
// runs out, or a read in that wait fails, the returned error wraps
// dns.ErrNotSettled: the write must not be repeated. NoWait skips that wait
// and returns as soon as the create succeeds.
func (c *Client) CreateLogAlarm(ctx context.Context, in *CreateLogAlarmInput) (*CreateLogAlarmOutput, error) {
	const op = "monitor.CreateLogAlarm"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LogProjectID", in.LogProjectID); err != nil {
		return nil, err
	}
	if err := checkLogAlarmChannelIDs(op, "InAlarm", in.InAlarm); err != nil {
		return nil, err
	}
	if err := checkLogAlarmChannelIDs(op, "OK", in.OK); err != nil {
		return nil, err
	}
	if err := checkLogAlarmQueryFilterPairing(op, in.QueryString, in.Filter); err != nil {
		return nil, err
	}

	severity := in.Severity
	if severity == "" {
		severity = LogAlarmSeverityLow
	}
	thresholdType := in.ThresholdType
	if thresholdType == "" {
		thresholdType = LogAlarmThresholdTypeFrequency
	}
	timeFrame := in.TimeFrame
	if timeFrame == 0 {
		timeFrame = 5
	}

	fields := logAlarmFields{
		Name:           in.Name,
		Description:    in.Description,
		Severity:       severity,
		LogProjectID:   in.LogProjectID,
		QueryString:    in.QueryString,
		LogSearchQuery: "[]",
		Filter:         in.Filter,
		ThresholdType:  thresholdType,
		Condition:      defaultLogAlarmCondition(in.Condition, thresholdType),
		ThresholdValue: *in.ThresholdValue,
		TimeFrame:      timeFrame,
		GroupByField:   in.GroupByField,
		AggField:       in.AggField,
		AggType:        in.AggType,
		InAlarm:        in.InAlarm,
		OK:             in.OK,
		Resend:         resolveLogAlarmResend(in.Resend),
	}

	if err := c.refuseIfLogAlarmNameExists(ctx, op, in.Name); err != nil {
		return nil, err
	}

	project, err := c.getLogProject(ctx, op, in.LogProjectID)
	if err != nil {
		return nil, err
	}
	fields.ProjectName = project.ProjectName

	body := buildLogAlarmBody(fields)
	var resp logAlarmWriteResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.alarmRoute([]string{"alarms", "logs"}, nil),
		Body:      body,
		OK:        []int{200, 201},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, wrapAmbiguousLogAlarmCreateErr(op, err)
	}

	if in.NoWait {
		return &CreateLogAlarmOutput{AlarmID: resp.ID}, nil
	}

	var alarm *Alarm
	var waitErr error
	if resp.ID != "" {
		alarm, waitErr = c.waitLogAlarmByID(ctx, op, resp.ID)
	} else {
		alarm, waitErr = c.waitLogAlarmByName(ctx, op, in.Name)
	}
	if alarm == nil {
		return &CreateLogAlarmOutput{AlarmID: resp.ID}, waitErr
	}
	return &CreateLogAlarmOutput{AlarmID: alarm.ID, Alarm: *alarm}, waitErr
}

// UpdateLogAlarmInput changes a log alarm identified by AlarmID. Every
// other field left nil keeps the alarm's current value. LogProjectID, when
// set, is re-read with GetLogProject for a fresh ProjectName, even when it
// names the same project the alarm already has. InAlarm and OK, when set
// to a non-nil empty slice, clear that channel list. QueryString and
// Filter, once merged with the read, must both be empty or both set, the
// same pairing rule CreateLogAlarm checks; leaving both nil skips that
// check and resends the read's exact pairing unchanged, even if it was
// never valid to create.
type UpdateLogAlarmInput struct {
	AlarmID string `vngcloud:"required"`
	NoWait  bool

	Name           *string
	Description    *string
	Severity       *string
	LogProjectID   *string
	QueryString    *string
	Filter         *json.RawMessage
	ThresholdType  *string
	Condition      *string
	ThresholdValue *float64
	TimeFrame      *int
	GroupByField   *string
	AggField       *string
	AggType        *string
	InAlarm        *[]string
	OK             *[]string
	Resend         *LogAlarmResend
}

// UpdateLogAlarmOutput is UpdateLogAlarm's result. Alarm is set once the
// post-update wait finds the alarm settled; NoWait skips that wait, leaving
// Alarm at its zero value.
type UpdateLogAlarmOutput struct {
	Alarm Alarm
}

// UpdateLogAlarm changes a log alarm. It checks AlarmID and any new
// LogProjectID, InAlarm, or OK channel ID with core.CheckPathID, reads the
// alarm with GetAlarm, and refuses with core.ErrInvalidInput, sending no
// PUT, when the read's Kind is not AlarmKindLog.
//
// It applies every set field onto the read, sends the merged body with
// buildLogAlarmBody (the same builder CreateLogAlarm uses, per ADR 0002
// rule 8), and sends one PUT. The PUT is a full replace and keeps the
// transport's normal retries, since resending it is safe.
//
// An unset QueryString resends the read's own logSearchQuery value exactly,
// rather than the "[]" CreateLogAlarm always sends: an alarm the console
// created through its own token-based search box may carry a different
// value there, which an update that does not touch QueryString must not
// silently overwrite. A set QueryString switches it to "[]", the SDK's own
// text-query convention.
//
// Without NoWait, UpdateLogAlarm waits the same way CreateLogAlarm does,
// always by AlarmID since it is already known. If the bound runs out, or a
// read in that wait fails, the returned error wraps dns.ErrNotSettled.
// NoWait skips that wait and returns as soon as the PUT succeeds.
func (c *Client) UpdateLogAlarm(ctx context.Context, in *UpdateLogAlarmInput) (*UpdateLogAlarmOutput, error) {
	const op = "monitor.UpdateLogAlarm"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "AlarmID", in.AlarmID); err != nil {
		return nil, err
	}
	if in.LogProjectID != nil {
		if err := core.CheckPathID(op, "LogProjectID", *in.LogProjectID); err != nil {
			return nil, err
		}
	}
	if in.InAlarm != nil {
		if err := checkLogAlarmChannelIDs(op, "InAlarm", *in.InAlarm); err != nil {
			return nil, err
		}
	}
	if in.OK != nil {
		if err := checkLogAlarmChannelIDs(op, "OK", *in.OK); err != nil {
			return nil, err
		}
	}

	current, err := c.getAlarm(ctx, op, in.AlarmID)
	if err != nil {
		return nil, err
	}
	if current.Kind != AlarmKindLog {
		return nil, fmt.Errorf("%w: %s: alarm %s is not a log alarm", core.ErrInvalidInput, op, in.AlarmID)
	}
	logDetail := LogAlarmDetail{}
	if current.Log != nil {
		logDetail = *current.Log
	}

	fields, err := c.mergeLogAlarmFields(ctx, op, in, current, logDetail)
	if err != nil {
		return nil, err
	}

	body := buildLogAlarmBody(fields)
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.alarmRoute([]string{"alarms", "logs", in.AlarmID}, nil),
		Body:      body,
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &UpdateLogAlarmOutput{}, nil
	}
	updated, waitErr := c.waitLogAlarmByID(ctx, op, in.AlarmID)
	if updated == nil {
		return &UpdateLogAlarmOutput{}, waitErr
	}
	return &UpdateLogAlarmOutput{Alarm: *updated}, waitErr
}

// mergeLogAlarmFields applies in's set fields onto current and logDetail,
// the alarm UpdateLogAlarm just read, and returns the merged
// logAlarmFields buildLogAlarmBody turns into a body. A new LogProjectID is
// re-read with GetLogProject for its ProjectName; otherwise logDetail's own
// LogProjectName is kept.
func (c *Client) mergeLogAlarmFields(ctx context.Context, op string, in *UpdateLogAlarmInput, current *Alarm, logDetail LogAlarmDetail) (logAlarmFields, error) {
	name := current.Name
	if in.Name != nil {
		name = *in.Name
	}
	description := current.Description
	if in.Description != nil {
		description = *in.Description
	}
	severity := current.Severity
	if in.Severity != nil {
		severity = *in.Severity
	}

	logProjectID := logDetail.LogProjectID
	projectName := logDetail.LogProjectName
	if in.LogProjectID != nil {
		logProjectID = *in.LogProjectID
		project, err := c.getLogProject(ctx, op, logProjectID)
		if err != nil {
			return logAlarmFields{}, err
		}
		projectName = project.ProjectName
	}

	queryString := logDetail.QueryString
	if in.QueryString != nil {
		queryString = *in.QueryString
	}
	filter := logDetail.Filter
	if in.Filter != nil {
		filter = *in.Filter
	}
	if in.QueryString != nil || in.Filter != nil {
		if err := checkLogAlarmQueryFilterPairing(op, queryString, filter); err != nil {
			return logAlarmFields{}, err
		}
	}
	logSearchQuery := logDetail.rawLogSearchQuery
	if in.QueryString != nil {
		logSearchQuery = "[]"
	}
	if logSearchQuery == "" {
		logSearchQuery = "[]"
	}

	thresholdType := logDetail.ThresholdType
	if in.ThresholdType != nil {
		thresholdType = *in.ThresholdType
	}
	condition := logDetail.Condition
	if in.Condition != nil {
		condition = *in.Condition
	}
	thresholdValue := logDetail.ThresholdValue
	if in.ThresholdValue != nil {
		thresholdValue = *in.ThresholdValue
	}
	timeFrame := logDetail.TimeFrame
	if in.TimeFrame != nil {
		timeFrame = *in.TimeFrame
	}
	groupByField := logDetail.GroupByField
	if in.GroupByField != nil {
		groupByField = *in.GroupByField
	}
	aggField := logDetail.AggField
	if in.AggField != nil {
		aggField = *in.AggField
	}
	aggType := logDetail.AggType
	if in.AggType != nil {
		aggType = *in.AggType
	}
	inAlarm := logDetail.InAlarm
	if in.InAlarm != nil {
		inAlarm = *in.InAlarm
	}
	ok := logDetail.OK
	if in.OK != nil {
		ok = *in.OK
	}
	resend := logDetail.Resend
	if in.Resend != nil {
		resend = *in.Resend
	}

	return logAlarmFields{
		Name:           name,
		Description:    description,
		Severity:       severity,
		LogProjectID:   logProjectID,
		ProjectName:    projectName,
		QueryString:    queryString,
		LogSearchQuery: logSearchQuery,
		Filter:         filter,
		ThresholdType:  thresholdType,
		Condition:      condition,
		ThresholdValue: thresholdValue,
		TimeFrame:      timeFrame,
		GroupByField:   groupByField,
		AggField:       aggField,
		AggType:        aggType,
		InAlarm:        inAlarm,
		OK:             ok,
		Resend:         resend,
	}, nil
}

// DeleteLogAlarmInput identifies the log alarm to delete.
type DeleteLogAlarmInput struct {
	AlarmID string `vngcloud:"required"`
}

type DeleteLogAlarmOutput struct{}

// DeleteLogAlarm deletes a log alarm and its history. There is no read
// first and no wait: the console treats a successful delete as done at
// once. DELETE is idempotent and keeps the transport's normal retries; a
// retry that finds the alarm already gone returns the SDK's not-found
// sentinel, the same as a genuine second delete.
func (c *Client) DeleteLogAlarm(ctx context.Context, in *DeleteLogAlarmInput) (*DeleteLogAlarmOutput, error) {
	const op = "monitor.DeleteLogAlarm"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "AlarmID", in.AlarmID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.alarmRoute([]string{"alarms", "logs", in.AlarmID}, nil),
		OK:        []int{200, 204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteLogAlarmOutput{}, nil
}
