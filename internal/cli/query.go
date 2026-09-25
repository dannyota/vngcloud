package cli

import (
	"encoding/json"

	"github.com/jmespath/go-jmespath"
)

// compileQuery compiles a --query expression, or returns (nil, nil) for an
// empty one. A bad expression is a usage error, checked by op.go before any
// request is sent, so a typo in --query never wastes a write.
func compileQuery(expr string) (*jmespath.JMESPath, error) {
	if expr == "" {
		return nil, nil //nolint:nilnil // no query is not an error; there is simply nothing to run
	}
	jp, err := jmespath.Compile(expr)
	if err != nil {
		return nil, newUsageError("--query: %s", err)
	}
	return jp, nil
}

// runQuery decodes data (JSON bytes from encodeJSON) into the generic value
// go-jmespath needs (objects and arrays, float64 numbers, strings, bools,
// and nil), then searches it when jp is non-nil. With jp nil it returns the
// decoded value unchanged, so render.go always has a generic value to work
// from whether or not --query was given.
func runQuery(jp *jmespath.JMESPath, data []byte) (any, error) {
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	if jp == nil {
		return generic, nil
	}
	return jp.Search(generic)
}
