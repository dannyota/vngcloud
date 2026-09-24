package core

const (
	DefaultPage     = 1
	DefaultPageSize = 10000
)

type ListOptions struct {
	Page int
	Size int
}

type Page struct {
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

type ListResult[T any] struct {
	Items []T
	Page  Page
}

// List is the output of a list operation whose API returns no page metadata.
type List[T any] struct {
	Items []T
}

// PagedList is the output of a list operation whose API returns page metadata.
type PagedList[T any] struct {
	Items     []T
	Page      int
	PageSize  int
	TotalPage int
	TotalItem int
}

func NewPagedList[T any](items []T, page, size, totalPage, totalItem int) *PagedList[T] {
	return &PagedList[T]{Items: items, Page: page, PageSize: size, TotalPage: totalPage, TotalItem: totalItem}
}
