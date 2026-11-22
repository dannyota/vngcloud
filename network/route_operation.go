package network

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"danny.vn/vngcloud/internal/floatingip"
)

func finishRouteOperation[T any](ctx context.Context, op string, out **T, resultErr *error) {
	values, scopeErr := floatingip.Credentials(ctx)
	raw, marshalErr := json.Marshal(*out)
	if scopeErr != nil || marshalErr != nil || floatingip.Reflects(raw, values) || natValueReflectsCredentials(reflect.ValueOf(*out), values) {
		if *out != nil {
			*resultErr = errors.Join(*resultErr, ErrNotSettled)
		}
		*out = nil
		*resultErr = errors.Join(*resultErr, invalidInventory(op))
	}
	*resultErr = sanitizeServerError(*resultErr, values)
}
