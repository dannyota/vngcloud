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
