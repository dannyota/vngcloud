package storage

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

func (c *ObjectClient) objectScalarHeader(h http.Header, name string, maxBytes int) string {
	var value string
	seen := false
	for key, values := range h {
		if !strings.EqualFold(key, name) {
			continue
		}
		if seen || len(values) != 1 {
			return ""
		}
		seen = true
		value = values[0]
	}
	return c.filterObjectMetadata(value, maxBytes)
}

func (c *ObjectClient) objectTime(value string, maxBytes int, layout string) *time.Time {
	if c.filterObjectMetadata(value, maxBytes) != value {
		return nil
	}
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	text, err := parsed.MarshalText()
	if err != nil {
		return nil
	}
	data, err := parsed.MarshalJSON()
	if err != nil {
		return nil
	}
	// The source bound does not bound normalized forms. Scan every finite
	// public representation before the time pointer can leave the client.
	for _, form := range []string{parsed.Format(time.RFC3339Nano), string(text), string(data), parsed.String(), parsed.GoString()} {
		if c.filterObjectMetadata(form, len(form)) != form {
			return nil
		}
	}
	return &parsed
}

func (c *ObjectClient) objectUserMetadata(h http.Header) map[string]string {
	const prefix = "x-amz-meta-"
	names := make([]string, 0, len(h))
	counts := make(map[string]int)
	for name := range h {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, prefix) {
			names = append(names, name)
			counts[lower]++
		}
	}
	out := make(map[string]string)
	total := 0
	// Valid suffixes are ASCII, so sorting normalized names selects the same
	// byte order regardless of the response's capitalization or map iteration.
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	for _, name := range names {
		lower := strings.ToLower(name)
		suffix := name[len(prefix):]
		key := lower[len(prefix):]
		values := h[name]
		if counts[lower] != 1 || !objectMetadataSuffix(suffix) || len(values) != 1 {
			continue
		}
		value := values[0]
		if c.filterObjectMetadata(suffix, 128) != suffix || c.filterObjectMetadata(key, 128) != key ||
			c.filterObjectMetadata(value, 2048) != value {
			continue
		}
		size := len(key) + len(value)
		if len(out) == 64 || size > 8192-total {
			continue
		}
		out[key] = value
		total += size
	}
	return out
}

func objectMetadataSuffix(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		letter := (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
		digit := b >= '0' && b <= '9'
		if !letter && !digit && b != '-' && b != '_' {
			return false
		}
	}
	return true
}
