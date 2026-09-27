package vngcloud

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

// redactedSecret is what every formatting and encoding method of Secret
// gives back, whatever verb, method, or encoding format asks for it.
const redactedSecret = "[redacted]"

// Secret holds a value the SDK must never print, log, or encode as itself:
// a private key, an access secret, or a client secret a create response
// returns once. A service that returns such a value, such as compute's
// CreateSSHKey, gives it this type rather than a plain string. Reveal is
// the only method that returns the underlying value; every other way to
// read a Secret, deliberate or accidental, gives back the same redacted
// text, so a caller that prints, logs, or encodes an Output holding one
// leaks nothing.
type Secret string

// String implements fmt.Stringer.
func (s Secret) String() string { return redactedSecret }

// GoString implements fmt.GoStringer. Format below already intercepts
// every fmt verb ahead of this method, %#v included, but a caller or
// another formatting package may still call GoString directly.
func (s Secret) GoString() string { return redactedSecret }

// Format implements fmt.Formatter, so every verb fmt supports (%v, %+v,
// %#v, %s, %q, %x, and so on) gives the redacted text. Without this method,
// %s and %q would print Secret's underlying string, since it is a defined
// string type.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(redactedSecret))
}

// LogValue implements slog.LogValuer, so a Secret passed to a slog call,
// directly or reached as a struct field through slog's reflection, logs the
// redacted text instead of its value.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(redactedSecret)
}

// MarshalJSON implements json.Marshaler, so json.Marshal of a Secret, or of
// a struct holding one, encodes the redacted text rather than the value.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(redactedSecret)
}

// MarshalText implements encoding.TextMarshaler, so an encoder that prefers
// it over MarshalJSON, such as encoding/xml or a text template, also gives
// the redacted text.
func (s Secret) MarshalText() ([]byte, error) {
	return []byte(redactedSecret), nil
}

// Reveal returns s's underlying value. It is the only method that does.
func (s Secret) Reveal() string { return string(s) }
