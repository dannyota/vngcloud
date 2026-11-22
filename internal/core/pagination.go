package core

import "fmt"

const (
	// DefaultPage is the default one-based page number.
	DefaultPage = 1
	// DefaultPageSize is the default list page size.
	DefaultPageSize = 10000
)

// maxDuplicateNameCheckPages bounds how many pages CheckNoDuplicateName
// walks before failing closed, so a broken or hostile server can never make
// a pre-write duplicate-name check loop forever. At DefaultPageSize, this
// covers 500,000 items, far more than any account holds.
const maxDuplicateNameCheckPages = 50

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

// NewPagedList wraps items with the returned pagination metadata.
func NewPagedList[T any](items []T, page, size, totalPage, totalItem int) *PagedList[T] {
	return &PagedList[T]{Items: items, Page: page, PageSize: size, TotalPage: totalPage, TotalItem: totalItem}
}

// CheckNoDuplicateName requires a complete inventory before accepting a name.
// identity, when supplied, validates nonempty unique resource IDs across pages.
func CheckNoDuplicateName[T any](op, noun, target string, name func(T) string, fetch func(page int) (*PagedList[T], error), identity ...func(T) string) error {
	incomplete := func() error {
		return fmt.Errorf("%w: %s: could not confirm no %s has the requested name: incomplete inventory", ErrInvalidInput, op, noun)
	}
	ids := make(map[string]bool)
	seen := 0
	totalPages, totalItems, pageSize := -1, -1, -1
	for page := DefaultPage; page <= maxDuplicateNameCheckPages; page++ {
		out, err := fetch(page)
		if err != nil {
			return err
		}
		if out == nil || out.Items == nil || out.Page != page || out.PageSize <= 0 || out.TotalPage < 0 || out.TotalItem < 0 || out.TotalPage > maxDuplicateNameCheckPages || len(out.Items) > out.PageSize {
			return incomplete()
		}
		if page == DefaultPage {
			totalPages, totalItems, pageSize = out.TotalPage, out.TotalItem, out.PageSize
		} else if totalPages != out.TotalPage || totalItems != out.TotalItem || pageSize != out.PageSize {
			return incomplete()
		}
		if len(out.Items) == 0 && (totalItems != 0 || page != DefaultPage || totalPages > 1) {
			return incomplete()
		}
		for _, item := range out.Items {
			if len(identity) > 0 {
				if identity[0] == nil {
					return incomplete()
				}
				id := identity[0](item)
				if id == "" || ids[id] {
					return incomplete()
				}
				ids[id] = true
			}
			if name(item) == target {
				return fmt.Errorf("%w: %s: a %s with the requested name already exists", ErrInvalidInput, op, noun)
			}
		}
		seen += len(out.Items)
		if seen > totalItems || totalPages == 0 && seen != 0 {
			return incomplete()
		}
		if page >= totalPages {
			if seen != totalItems {
				return incomplete()
			}
			return nil
		}
	}
	return incomplete()
}
