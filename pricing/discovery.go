package pricing

import (
	"encoding/json"
	"errors"
	"time"
)

// DiscoveryPrice preserves unknown amounts as nil. Source names an operation
// or a first-party document, never a request URL.
type DiscoveryPrice struct {
	Status          string
	Basis           string
	Currency        string
	Tax             string
	Source          string
	MonthlyPrice    *float64
	OriginalPrice   *float64
	DiscountPrice   *float64
	DiscountPercent *float64
}

// DiscoveryMetadata describes coverage of one account-scoped invocation.
// QuotedAt is UTC completion time, not an atomic catalog snapshot.
type DiscoveryMetadata struct {
	Region         string
	Complete       bool
	Requests       int
	QuotedAt       time.Time
	Failures       []DiscoveryFailure
	Notes          []PricingNote
	ExcludedGroups *int
}

// DiscoveryFailure contains only trusted scope and safe error classification.
type DiscoveryFailure struct {
	Operation string
	ItemKey   string
	Code      string
	Status    int
}

// PricingNote distinguishes published rules from observed account behavior.
type PricingNote struct {
	Topic     string
	Scope     string
	Text      string
	SourceURL string
	Evidence  string
}

var ErrDiscoveryIncomplete = errors.New("pricing discovery incomplete")

// DiscoveryError retains the original causes of a partial invocation.
// Errors must contain only errors from guarded exchanges or local validation.
type DiscoveryError struct{ Errors []error }

func (e *DiscoveryError) Error() string        { return ErrDiscoveryIncomplete.Error() }
func (e *DiscoveryError) Is(target error) bool { return target == ErrDiscoveryIncomplete }
func (e *DiscoveryError) Unwrap() []error      { return e.Errors }

// MarshalJSON preserves empty lists and normalizes completion time to UTC.
func (m DiscoveryMetadata) MarshalJSON() ([]byte, error) {
	type metadata DiscoveryMetadata
	if m.Failures == nil {
		m.Failures = []DiscoveryFailure{}
	}
	if m.Notes == nil {
		m.Notes = []PricingNote{}
	}
	m.QuotedAt = m.QuotedAt.UTC()
	return json.Marshal(metadata(m))
}
