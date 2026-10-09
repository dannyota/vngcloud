package core

import (
	"errors"
	"testing"
)

type namedItem struct {
	name string
}

func TestCheckNoDuplicateNameMatchOnFirstPage(t *testing.T) {
	fetch := func(page int) (*PagedList[namedItem], error) {
		if page != DefaultPage {
			t.Fatalf("fetch called with page %d, want %d", page, DefaultPage)
		}
		return NewPagedList([]namedItem{{"a"}, {"web-1"}}, 1, 2, 1, 2), nil
	}
	err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCheckNoDuplicateNameNoMatchSinglePage(t *testing.T) {
	fetch := func(page int) (*PagedList[namedItem], error) {
		return NewPagedList([]namedItem{{"a"}, {"b"}}, 1, 2, 1, 2), nil
	}
	if err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestCheckNoDuplicateNameEmptyList(t *testing.T) {
	fetch := func(page int) (*PagedList[namedItem], error) {
		return NewPagedList[namedItem](nil, 1, 10000, 0, 0), nil
	}
	if err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

// TestCheckNoDuplicateNameWalksEveryPage checks that a match on a later
// page is still caught: a check that only looked at the first page would
// silently allow a duplicate name past it.
func TestCheckNoDuplicateNameWalksEveryPage(t *testing.T) {
	pages := [][]namedItem{
		{{"a"}, {"b"}},
		{{"c"}, {"web-1"}},
		{{"d"}, {"e"}},
	}
	var fetched []int
	fetch := func(page int) (*PagedList[namedItem], error) {
		fetched = append(fetched, page)
		items := pages[page-1]
		return NewPagedList(items, page, 2, len(pages), 6), nil
	}
	err := CheckNoDuplicateName("op", "volume", "web-1", func(i namedItem) string { return i.name }, fetch)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if len(fetched) != 2 {
		t.Fatalf("fetched pages = %v, want 2 (stops once the match is found)", fetched)
	}
}

// TestCheckNoDuplicateNameFailsClosedOnMissingTotalPage checks that a
// non-empty page reporting no page count refuses rather than treating an
// unprovable list as empty of duplicates.
func TestCheckNoDuplicateNameFailsClosedOnMissingTotalPage(t *testing.T) {
	fetch := func(page int) (*PagedList[namedItem], error) {
		return NewPagedList([]namedItem{{"a"}}, 1, 10000, 0, 1), nil
	}
	err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestCheckNoDuplicateNameFailsClosedOnUnderreportedTotal checks that a
// mismatch between the items actually collected and the list's own
// TotalItem refuses, rather than trusting a short collection.
func TestCheckNoDuplicateNameFailsClosedOnUnderreportedTotal(t *testing.T) {
	fetch := func(page int) (*PagedList[namedItem], error) {
		return NewPagedList([]namedItem{{"a"}, {"b"}}, 1, 2, 1, 5), nil
	}
	err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestCheckNoDuplicateNameCapsPages checks that the walk fails closed
// rather than looping forever when a broken server reports far more pages
// than any account could have.
func TestCheckNoDuplicateNameCapsPages(t *testing.T) {
	calls := 0
	fetch := func(page int) (*PagedList[namedItem], error) {
		calls++
		return NewPagedList([]namedItem{{"a"}}, page, 1, 1000000, 1000000), nil
	}
	err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if calls > maxDuplicateNameCheckPages+1 {
		t.Fatalf("fetch called %d times, want at most %d", calls, maxDuplicateNameCheckPages+1)
	}
}

func TestCheckNoDuplicateNameFetchErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	fetch := func(page int) (*PagedList[namedItem], error) {
		return nil, wantErr
	}
	if err := CheckNoDuplicateName("op", "server", "web-1", func(i namedItem) string { return i.name }, fetch); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}
