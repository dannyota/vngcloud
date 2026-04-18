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
