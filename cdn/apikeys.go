package cdn

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
)

// APIKey is one vCDN API key of the account. Times are RFC 3339. The server
// also sends each key's token and the account email; the model has no field
// for them. Current marks the key this client sends.
type APIKey struct {
	ID                int       `json:"apiKeyId"`
	ExpiresAt         time.Time `json:"expiredDate"`
	CreateTime        time.Time `json:"createTime"`
	UpdateTime        time.Time `json:"updateTime"`
	AllowOriginHeader string    `json:"allowOriginHeader"`
	Current           bool      `json:"current"`
}

// apiKeyWire is the server's key object. Token is read only to set Current
// and is never copied to an APIKey.
type apiKeyWire struct {
	ID                int       `json:"apiKeyId"`
	Token             string    `json:"token"`
	ExpiredDate       time.Time `json:"expiredDate"`
	CreateTime        time.Time `json:"createTime"`
	UpdateTime        time.Time `json:"updateTime"`
	AllowOriginHeader string    `json:"allowOriginHeader"`
}

// ListAPIKeysInput has no fields today; a nil Input is valid.
type ListAPIKeysInput struct{}

type ListAPIKeysOutput = core.List[APIKey]

// ListAPIKeys lists every API key of the account. Exactly the key this
// client sends has Current set, so a caller can find the expiry of the key
// in use. It needs an API key (ErrNoAPIKey otherwise).
//
// The server sends every key's token in this answer, so any holder of one
// key can read the others. The request is marked sensitive, the comparison
// runs in constant time in memory, and the tokens are dropped before the
// call returns. A data that is not a list is an EmptyResponse error.
func (c *Client) ListAPIKeys(ctx context.Context, in *ListAPIKeysInput) (*ListAPIKeysOutput, error) {
	const op = "cdn.ListAPIKeys"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	r := call{op: op, method: http.MethodGet, parts: []string{"apikey", "list"}, sensitive: true}
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	wire, err := decodeList[apiKeyWire](r, data, false)
	if err != nil {
		return nil, err
	}
	key, err := c.c.CDNAPIKey()
	if err != nil {
		return nil, err
	}
	items := make([]APIKey, len(wire))
	for i, w := range wire {
		items[i] = APIKey{
			ID:                w.ID,
			ExpiresAt:         w.ExpiredDate,
			CreateTime:        w.CreateTime,
			UpdateTime:        w.UpdateTime,
			AllowOriginHeader: w.AllowOriginHeader,
			Current:           w.Token != "" && subtle.ConstantTimeCompare([]byte(w.Token), []byte(key)) == 1,
		}
	}
	return &ListAPIKeysOutput{Items: items}, nil
}
