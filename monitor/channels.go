package monitor

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// ListChannelTypesInput has no fields today; a nil Input is valid. Fields
// may be added later without breaking callers.
type ListChannelTypesInput struct{}

type ListChannelTypesOutput = core.List[ChannelType]

// ListChannelTypes lists the notification channel types the account may
// create a Channel for. There is no paging.
func (c *Client) ListChannelTypes(ctx context.Context, in *ListChannelTypesInput) (*ListChannelTypesOutput, error) {
	const op = "monitor.ListChannelTypes"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var resp struct {
		LstData []ChannelType `json:"lstData"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.notificationRoute([]string{"type", "list"}, nil),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return &ListChannelTypesOutput{Items: resp.LstData}, nil
}

// ListChannelsInput filters the channel list. Type restricts it to one
// channel type, empty for every type. Page and Size page the result; a
// non-positive value sends core.DefaultPage and core.DefaultPageSize, as
// core.PageQuery does for other services. The API also takes searchtext and
// field query keys for a name search the SDK does not expose yet; the SDK
// always sends them empty, as the console does outside a name search.
type ListChannelsInput struct {
	Type string
	Page int
	Size int
}

type ListChannelsOutput = core.PagedList[Channel]

// ListChannels lists notification channels on the account. A nil Input is
// valid and lists every type from core.DefaultPage at core.DefaultPageSize,
// the same as &ListChannelsInput{}.
func (c *Client) ListChannels(ctx context.Context, in *ListChannelsInput) (*ListChannelsOutput, error) {
	const op = "monitor.ListChannels"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	var typ string
	var page, size int
	if in != nil {
		typ, page, size = in.Type, in.Page, in.Size
	}
	q := core.PageQuery(page, size)
	q.Set("searchtext", "")
	q.Set("field", "")
	q.Set("type", typ)

	var resp listChannelsResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.notificationRoute([]string{"notification", "list", "typeSearch"}, q),
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	return core.NewPagedList(resp.LstData, resp.Page, resp.PageSize, resp.TotalPage, resp.TotalItem), nil
}

type listChannelsResponse struct {
	LstData   []Channel `json:"lstData"`
	Page      int       `json:"page"`
	PageSize  int       `json:"pageSize"`
	TotalPage int       `json:"totalPage"`
	TotalItem int       `json:"totalItem"`
}

// GetChannelInput identifies the channel to read.
type GetChannelInput struct {
	ChannelID string `vngcloud:"required"`
}

type GetChannelOutput struct {
	Channel Channel
}

// GetChannel finds a channel by ID. There is no get-by-ID call, so
// GetChannel lists every page at core.DefaultPageSize and returns the item
// whose ID matches. ChannelID never reaches a URL path, so it needs no
// core.CheckPathID check: nothing here can send it to a different path than
// the caller named. No channel with that ID, including on an account with
// none at all, returns an error wrapping core.ErrNotFound.
func (c *Client) GetChannel(ctx context.Context, in *GetChannelInput) (*GetChannelOutput, error) {
	const op = "monitor.GetChannel"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	for page := 1; ; page++ {
		list, err := c.ListChannels(ctx, &ListChannelsInput{Page: page, Size: core.DefaultPageSize})
		if err != nil {
			return nil, err
		}
		for _, ch := range list.Items {
			if ch.ID == in.ChannelID {
				return &GetChannelOutput{Channel: ch}, nil
			}
		}
		if page >= list.TotalPage {
			break
		}
	}
	return nil, fmt.Errorf("%w: channel %s", core.ErrNotFound, in.ChannelID)
}
