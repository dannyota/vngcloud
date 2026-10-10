package backup

import (
	"encoding/json"
	"errors"
)

type collection[T any] struct {
	Items       []T  `json:"items"`
	Page        *int `json:"page"`
	PageSize    *int `json:"pageSize"`
	TotalPages  *int `json:"totalPages"`
	TotalItems  *int `json:"totalItems"`
	pagePresent bool
	sizePresent bool
}

func (c *collection[T]) UnmarshalJSON(data []byte) error {
	type wire collection[T]
	var w wire
	if err := json.Unmarshal(data, &w); err != nil {
		return errors.New("invalid Backup Center collection")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return errors.New("invalid Backup Center collection")
	}
	_, w.pagePresent = fields["page"]
	_, w.sizePresent = fields["pageSize"]
	*c = collection[T](w)
	return nil
}

func (c collection[T]) valid(paged bool) bool {
	return c.Items != nil && c.pagePresent && c.sizePresent && c.TotalPages != nil && c.TotalItems != nil && (!paged || (c.Page != nil && c.PageSize != nil))
}
