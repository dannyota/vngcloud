package cdn

import (
	"context"
	"encoding/json"
	"net/http"

	"danny.vn/vngcloud/internal/core"
)

// ListWebAcceleratorsInput has no fields today; a nil Input is valid.
type ListWebAcceleratorsInput struct{}

type ListWebAcceleratorsOutput = core.List[WebAcceleratorSummary]

// ListWebAccelerators lists every Web Accelerator CDN of the account in one
// call. The API has no paging, and an account with none gives empty Items.
// It needs an API key (ErrNoAPIKey otherwise).
func (c *Client) ListWebAccelerators(ctx context.Context, in *ListWebAcceleratorsInput) (*ListWebAcceleratorsOutput, error) {
	const op = "cdn.ListWebAccelerators"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	r := call{op: op, method: http.MethodGet, parts: []string{"cdn", "list"}}
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, err
	}
	items, err := decodeList[WebAcceleratorSummary](r, data, true)
	if err != nil {
		return nil, err
	}
	return &ListWebAcceleratorsOutput{Items: items}, nil
}

// GetWebAcceleratorInput identifies the CDN to read.
type GetWebAcceleratorInput struct {
	CDNID string `vngcloud:"required"`
}

type GetWebAcceleratorOutput struct {
	WebAccelerator WebAccelerator
}

// GetWebAccelerator reads one Web Accelerator CDN. An unknown or malformed
// ID gives an error that matches ErrNotFound.
func (c *Client) GetWebAccelerator(ctx context.Context, in *GetWebAcceleratorInput) (*GetWebAcceleratorOutput, error) {
	const op = "cdn.GetWebAccelerator"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "CDNID", in.CDNID); err != nil {
		return nil, err
	}
	_, wa, err := c.readCDN(ctx, op, in.CDNID)
	if err != nil {
		return nil, err
	}
	return &GetWebAcceleratorOutput{WebAccelerator: *wa}, nil
}

// readCDN reads the detail of cdnID under the operation name op, so an
// error names the call the read happened inside. It returns the raw object,
// which an update sends back, and the decoded model.
func (c *Client) readCDN(ctx context.Context, op, cdnID string) (json.RawMessage, *WebAccelerator, error) {
	r := call{op: op, method: http.MethodGet, parts: []string{"cdn", "detail", cdnID}, notFound: "no such CDN"}
	data, err := c.do(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	var wa WebAccelerator
	if emptyData(data) || decodeObject(data, &wa) != nil {
		return nil, nil, r.unexpected(http.StatusOK)
	}
	return data, &wa, nil
}
