package iam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// epochMillis decodes a timestamp the accounts and policies APIs send in
// several forms, sometimes across rows of one response: a plain
// epoch-milliseconds number, the same digits as a string, an object
// {"$numberLong": "<digits>"}, an ISO 8601 string such as
// "2025-06-15T15:06:40.000Z", or the key left out entirely. A field routed
// through epochMillis, via the alias-struct pattern each model's
// UnmarshalJSON uses, reads as 0 when the key is missing, null, or an empty
// string. Any other shape is an error, so an unknown form is never mistaken
// for "no timestamp".
type epochMillis int64

func (m *epochMillis) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" {
		*m = 0
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*m = epochMillis(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		ms, err := parseTimestampString(s)
		if err != nil {
			return err
		}
		*m = epochMillis(ms)
		return nil
	}
	var wrapped struct {
		NumberLong string `json:"$numberLong"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return fmt.Errorf("iam: timestamp: unsupported form: %w", err)
	}
	ms, err := strconv.ParseInt(wrapped.NumberLong, 10, 64)
	if err != nil {
		return fmt.Errorf("iam: timestamp: %w", err)
	}
	*m = epochMillis(ms)
	return nil
}

// parseTimestampString reads a string timestamp: empty, digits of epoch
// milliseconds, or RFC 3339 with or without fractional seconds.
func parseTimestampString(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ms, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, fmt.Errorf("iam: timestamp: string is neither digits nor RFC 3339: %w", err)
	}
	return t.UnixMilli(), nil
}
