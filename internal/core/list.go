package core

import (
	"net/url"
	"strconv"
)

func ListQuery(opts *ListOptions) url.Values {
	page, size := DefaultPage, DefaultPageSize
	if opts != nil {
		if opts.Page > 0 {
			page = opts.Page
		}
		if opts.Size > 0 {
			size = opts.Size
		}
	}
	return PageQuery(page, size)
}

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

func PageResult[T any](items []T, page, size, totalPage, totalItem int) *ListResult[T] {
	return &ListResult[T]{
		Items: items,
		Page: Page{
			Page:      page,
			PageSize:  size,
			TotalPage: totalPage,
			TotalItem: totalItem,
		},
	}
}
