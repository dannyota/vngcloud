package iam

type pageList[T any] struct {
	Items      []T
	PageNumber *int
	PageSize   *int
	TotalItems *int
	TotalPages *int
}

type pageResponse[T any] struct {
	Data       []T  `json:"data"`
	PageNumber *int `json:"pageNumber"`
	PageSize   *int `json:"pageSize"`
	TotalItems *int `json:"totalItems"`
	TotalPages *int `json:"totalPages"`
}

func (p pageResponse[T]) output() *pageList[T] {
	return &pageList[T]{Items: p.Data, PageNumber: p.PageNumber, PageSize: p.PageSize, TotalItems: p.TotalItems, TotalPages: p.TotalPages}
}
