package transport

import (
	"log/slog"
	"net/http"
	"time"
)

// Config sets the HTTP transport, token source, retries, and observation hooks.
type Config struct {
	// HTTPClient defaults to a client with a 120-second timeout when nil.
	HTTPClient *http.Client
	// TokenSource supplies tokens for authenticated requests.
	TokenSource TokenSource
	// RetryCount sets the retry limit; negative values select zero.
	RetryCount int
	// RetryInterval defaults to one second when not positive.
	RetryInterval time.Duration
	// UserAgent is sent as the request's User-Agent header when nonempty.
	UserAgent string
	// Capture receives eligible request and response captures when set.
	Capture CaptureFunc

	// Logger, when set, receives one Debug record named "request" per HTTP
	// attempt, with the method, the URL path without its query string, the
	// status (omitted when no response was received), and the duration. A
	// nil Logger logs nothing.
	Logger *slog.Logger
}
