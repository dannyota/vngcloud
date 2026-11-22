package core

import (
	"errors"
	"testing"
)

func TestFlexibleListRequiresExplicitArray(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"data":null}`, `{"data":{}}`, `{"data":[],"items":[]}`, `{"data":[],"Data":[1]}`, `{"data":[],"data":[1]}`, `{"data":[],"page":null}`, `{"data":[],"page":-1}`, `{"data":[],"size":10,"pageSize":20}`, `{"data":[],"total":1,"totalItems":2}`} {
		if _, _, _, _, _, err := DecodeFlexibleList[int]([]byte(body)); err == nil {
			t.Errorf("accepted malformed list %s", body)
		}
	}
	for _, body := range []string{`[]`, `{"items":[]}`, `{"data":[1],"page":1,"size":1,"total":1,"totalPages":1}`} {
		items, _, _, _, _, err := DecodeFlexibleList[int]([]byte(body))
		if err != nil || items == nil {
			t.Fatalf("explicit list rejected: %s", body)
		}
	}
}

func TestFlexibleListRejectsResourceKeyAliases(t *testing.T) {
	type row struct {
		ID string `json:"id"`
	}
	for _, body := range []string{`[{"id":"one","ID":"two"}]`, `{"items":[{"id":"one","id":"two"}]}`} {
		if _, _, _, _, _, err := DecodeFlexibleList[row]([]byte(body)); err == nil {
			t.Fatal("resource identity alias accepted")
		}
	}
}

func TestDuplicateGuardRejectsIncompleteInventory(t *testing.T) {
	for _, scenario := range []string{"nil-output", "missing-list", "repeated-page", "changing-total", "changing-page-count", "empty-middle", "negative-total", "wrong-page-size"} {
		t.Run(scenario, func(t *testing.T) {
			fetch := func(page int) (*PagedList[namedItem], error) {
				out := NewPagedList([]namedItem{{name: "other"}}, page, 1, 2, 2)
				switch scenario {
				case "nil-output":
					return nil, nil
				case "missing-list":
					return NewPagedList[namedItem](nil, 1, 1, 0, 0), nil
				case "repeated-page":
					out.Page = 1
				case "changing-total":
					if page == 1 {
						out.TotalItem = 3
					}
				case "changing-page-count":
					if page == 1 {
						out.TotalPage = 3
					}
				case "empty-middle":
					if page == 1 {
						out.Items = []namedItem{}
						out.TotalItem = 1
					}
				case "negative-total":
					out.TotalItem = -1
				case "wrong-page-size":
					out.PageSize = 0
				}
				return out, nil
			}
			if scenario == "nil-output" {
				defer func() {
					if recover() != nil {
						t.Error("nil inventory panicked")
					}
				}()
			}
			err := CheckNoDuplicateName("create", "resource", "target", func(i namedItem) string { return i.name }, fetch, func(i namedItem) string { return i.name })
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatal("incomplete inventory authorized create")
			}
		})
	}
}

func TestDuplicateGuardRequiresUniqueResourceIDs(t *testing.T) {
	type resource struct{ id, name string }
	for _, missing := range []bool{false, true} {
		fetch := func(page int) (*PagedList[resource], error) {
			id := "same-id"
			if missing {
				id = ""
			}
			return NewPagedList([]resource{{id: id, name: "other"}}, page, 1, 2, 2), nil
		}
		err := CheckNoDuplicateName("create", "resource", "target", func(r resource) string { return r.name }, fetch, func(r resource) string { return r.id })
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatal("missing or repeated resource ID authorized create")
		}
	}
}

func TestPagedInventoryDecoderRequiresPresentCounts(t *testing.T) {
	for _, body := range []string{`{"listData":[],"page":1,"pageSize":10,"totalPage":0}`, `{"listData":[],"page":1,"pageSize":10,"totalItem":0}`, `{"listData":[],"page":1,"pageSize":10,"totalPage":0,"totalItem":null}`, `{"listData":null,"page":1,"pageSize":10,"totalPage":0,"totalItem":0}`} {
		if _, err := DecodePagedList[int]([]byte(body)); err == nil {
			t.Fatal("missing inventory evidence accepted")
		}
	}
	out, err := DecodePagedList[int]([]byte(`{"listData":[],"page":1,"pageSize":10,"totalPage":0,"totalItem":0}`))
	if err != nil || out.Items == nil || len(out.Items) != 0 {
		t.Fatal("explicit empty inventory rejected")
	}
}
