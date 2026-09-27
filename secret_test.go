package vngcloud_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

// fixtureSecret stands in for a value the SDK must never print, log, or
// encode: distinctive enough that any test failure below names exactly
// what leaked.
const fixtureSecret = "top-secret-value-9f3a"

func TestSecretFmtVerbs(t *testing.T) {
	s := vngcloud.Secret(fixtureSecret)
	type wrapper struct {
		Secret vngcloud.Secret
	}
	w := wrapper{Secret: s}

	forms := map[string]string{
		"%v on Secret":   fmt.Sprintf("%v", s),
		"%+v on Secret":  fmt.Sprintf("%+v", s),
		"%#v on Secret":  fmt.Sprintf("%#v", s),
		"%s on Secret":   fmt.Sprintf("%s", s),
		"%q on Secret":   fmt.Sprintf("%q", s),
		"%v on wrapper":  fmt.Sprintf("%v", w),
		"%+v on wrapper": fmt.Sprintf("%+v", w),
		"%#v on wrapper": fmt.Sprintf("%#v", w),
	}
	for name, got := range forms {
		if strings.Contains(got, fixtureSecret) {
			t.Fatalf("%s = %q, holds the fixture secret", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s = %q, want it to contain [redacted]", name, got)
		}
	}
}

func TestSecretStringAndGoString(t *testing.T) {
	s := vngcloud.Secret(fixtureSecret)
	if got := s.String(); got != "[redacted]" {
		t.Fatalf("String() = %q, want [redacted]", got)
	}
	if got := s.GoString(); got != "[redacted]" {
		t.Fatalf("GoString() = %q, want [redacted]", got)
	}
}

func TestSecretLogValue(t *testing.T) {
	s := vngcloud.Secret(fixtureSecret)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("event", "value", s)

	got := buf.String()
	if strings.Contains(got, fixtureSecret) {
		t.Fatalf("slog output = %q, holds the fixture secret", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("slog output = %q, want it to contain [redacted]", got)
	}
}

func TestSecretMarshalJSON(t *testing.T) {
	s := vngcloud.Secret(fixtureSecret)
	type wrapper struct {
		Secret vngcloud.Secret `json:"secret"`
	}

	for name, v := range map[string]any{"Secret": s, "wrapper": wrapper{Secret: s}} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal(%s) error = %v", name, err)
		}
		if strings.Contains(string(data), fixtureSecret) {
			t.Fatalf("json.Marshal(%s) = %s, holds the fixture secret", name, data)
		}
		if !strings.Contains(string(data), "[redacted]") {
			t.Fatalf("json.Marshal(%s) = %s, want it to contain [redacted]", name, data)
		}
	}
}

func TestSecretMarshalText(t *testing.T) {
	s := vngcloud.Secret(fixtureSecret)
	data, err := s.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	if string(data) != "[redacted]" {
		t.Fatalf("MarshalText() = %q, want [redacted]", data)
	}
}

func TestSecretReveal(t *testing.T) {
	s := vngcloud.Secret(fixtureSecret)
	if got := s.Reveal(); got != fixtureSecret {
		t.Fatalf("Reveal() = %q, want %q", got, fixtureSecret)
	}
}
