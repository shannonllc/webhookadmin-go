package webhookadmin

import (
	"context"
	"net/url"
	"time"
)

// Poller fetches messages from a polling endpoint, as the receiver, with a poller token (sk_poll_…, not an API key).
// It is safe for concurrent use.
//
// Delivery is at least once: calling again with the returned Iterator acknowledges the previous page, and calling
// with an older iterator returns the same messages again.
//
//	poller, err := webhookadmin.NewPoller("ep_...", "sk_poll_...")
//	it := poller.Pages(&webhookadmin.PollParams{Iterator: saved})
//	for it.Next(ctx) {
//		page := it.Current()
//		for _, m := range page.Data { handle(m.Payload) }
//		saved = page.Iterator // save it to resume from here
//	}
//	if err := it.Err(); err != nil { … }
type Poller struct {
	c          *Client
	endpointID string
}

// NewPoller creates a poller for one polling endpoint. token is a poller token of that endpoint, from
// Endpoints.CreatePollerToken or the dashboard. The client options (WithBaseURL, WithTimeout, WithMaxRetries,
// WithHTTPClient) apply.
func NewPoller(endpointID, token string, opts ...Option) (*Poller, error) {
	if endpointID == "" {
		return nil, newError(KindInvalidArgument, "Missing endpoint ID. Pass the ID of the polling endpoint (ep_…).", nil)
	}
	if token == "" {
		return nil, newError(KindInvalidArgument, "Missing token. Pass a poller token of the endpoint (sk_poll_…).", nil)
	}
	c, _ := NewClient(token, opts...) // fails only when the key is empty, checked above
	return &Poller{c: c, endpointID: endpointID}, nil
}

// EndpointID returns the polling endpoint.
func (p *Poller) EndpointID() string { return p.endpointID }

// BaseURL returns the API origin without a trailing slash.
func (p *Poller) BaseURL() string { return p.c.baseURL }

// Poll fetches one page. It is retried on 429, 5xx and connection errors like any other read.
func (p *Poller) Poll(ctx context.Context, params *PollParams, opts ...RequestOption) (*PollPage, error) {
	q := derefOr(params)
	var out PollPage
	query := url.Values{"iterator": {q.Iterator}, "limit": {limitString(q.Limit)}, "event_type": q.EventTypes}
	if !q.After.IsZero() {
		query.Set("after", q.After.UTC().Format(time.RFC3339Nano))
	}
	if err := p.c.do(ctx, request{method: "GET", path: "/v1/poller/" + esc(p.endpointID), query: query, idempotent: true}, &out, p.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Pages fetches pages from params.Iterator (with the same Limit, EventTypes and After) until a page with Done true, which is returned too.
// Each call acknowledges the page before it, so save Current().Iterator after processing a page to resume later.
// The last page is acknowledged by the next call (the next time you poll with the saved iterator).
func (p *Poller) Pages(params *PollParams, opts ...RequestOption) *PollIter {
	return &PollIter{p: p, params: derefOr(params), opts: opts}
}

// Messages walks every message of Pages, one by one. Use Pages when you need the iterator to resume later.
func (p *Poller) Messages(params *PollParams, opts ...RequestOption) *Iter[PolledMessage] {
	q := derefOr(params)
	return newIter(q.Iterator, func(ctx context.Context, iterator string) (*Page[PolledMessage], error) {
		pp := q
		pp.Iterator = iterator
		page, err := p.Poll(ctx, &pp, opts...)
		if err != nil {
			return nil, err
		}
		next := page.Iterator
		if page.Done {
			next = ""
		}
		return &Page[PolledMessage]{Items: page.Data, NextCursor: next}, nil
	})
}

// PollIter walks the pages of a polling endpoint:
//
//	it := poller.Pages(nil)
//	for it.Next(ctx) {
//		page := it.Current()
//	}
//	if err := it.Err(); err != nil { … }
type PollIter struct {
	p      *Poller
	params PollParams
	opts   []RequestOption
	cur    *PollPage
	err    error
	done   bool
}

// Next fetches the next page. It returns false after the page with Done true, or on an error.
func (it *PollIter) Next(ctx context.Context) bool {
	if it.done || it.err != nil {
		return false
	}
	page, err := it.p.Poll(ctx, &it.params, it.opts...)
	if err != nil {
		it.err = err
		return false
	}
	it.cur, it.params.Iterator, it.done = page, page.Iterator, page.Done
	return true
}

// Current returns the page Next fetched.
func (it *PollIter) Current() *PollPage { return it.cur }

// Err returns the error that stopped the iteration, if any.
func (it *PollIter) Err() error { return it.err }
