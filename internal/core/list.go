package core

import (
	"net/url"
	"strconv"
)

// PageQuery builds the page and size query parameters, substituting the
// defaults for a page or size that is not positive.
func PageQuery(page, size int) url.Values {
	if page <= 0 {
		page = DefaultPage
	}
	if size <= 0 {
		size = DefaultPageSize
	}
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(size))
	return q
}
