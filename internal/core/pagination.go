package core

import "fmt"

const (
	DefaultPage     = 1
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

func NewPagedList[T any](items []T, page, size, totalPage, totalItem int) *PagedList[T] {
	return &PagedList[T]{Items: items, Page: page, PageSize: size, TotalPage: totalPage, TotalItem: totalItem}
}

// CheckNoDuplicateName walks every page fetch returns, starting at
// DefaultPage, calling name on each item along the way. It returns an error
// wrapping ErrInvalidInput naming noun and target the moment one matches
// target exactly, for a paid create's pre-write duplicate-name check: a
// rerun after an unclear failure must never risk ordering a second resource
// under a name that already exists.
//
// It also fails closed, with the same error, whenever a page's own
// metadata cannot prove the walk actually covered every item: a non-empty
// page reporting TotalPage 0 or less, more pages than
// maxDuplicateNameCheckPages, or a final count that does not match
// TotalItem. Trusting an incomplete list as "no duplicate" would be the
// same hole as skipping the check entirely.
func CheckNoDuplicateName[T any](op, noun, target string, name func(T) string, fetch func(page int) (*PagedList[T], error)) error {
	seen := 0
	for page := DefaultPage; ; page++ {
		if page > maxDuplicateNameCheckPages {
			return fmt.Errorf("%w: %s: could not confirm no %s is named %q: the list has more than %d pages",
				ErrInvalidInput, op, noun, target, maxDuplicateNameCheckPages)
		}
		out, err := fetch(page)
		if err != nil {
			return err
		}
		for _, item := range out.Items {
			if name(item) == target {
				return fmt.Errorf("%w: %s: a %s named %q already exists", ErrInvalidInput, op, noun, target)
			}
		}
		seen += len(out.Items)
		if out.TotalPage <= 0 {
			if len(out.Items) > 0 || out.TotalItem > 0 {
				return fmt.Errorf("%w: %s: could not confirm no %s is named %q: the list response carried no page count",
					ErrInvalidInput, op, noun, target)
			}
			return nil
		}
		if page >= out.TotalPage {
			if seen != out.TotalItem {
				return fmt.Errorf("%w: %s: could not confirm no %s is named %q: listed %d of a reported %d total",
					ErrInvalidInput, op, noun, target, seen, out.TotalItem)
			}
			return nil
		}
	}
}
