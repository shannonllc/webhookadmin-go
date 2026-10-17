package webhookadmin

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

func esc(id string) string { return url.PathEscape(id) }

func sinceString(ms int64) string {
	if ms == 0 {
		return ""
	}
	return strconv.FormatInt(ms, 10)
}

func limitString(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// Iter walks every item across pages:
//
//	it := client.Messages.ListAll(&webhookadmin.MessageListParams{Status: webhookadmin.MessageFailed})
//	for it.Next(ctx) {
//		m := it.Current()
//	}
//	if err := it.Err(); err != nil { … }
type Iter[T any] struct {
	fetch  func(ctx context.Context, cursor string) (*Page[T], error)
	cursor string
	items  []T
	idx    int
	cur    T
	err    error
	last   bool
}

func newIter[T any](cursor string, fetch func(ctx context.Context, cursor string) (*Page[T], error)) *Iter[T] {
	return &Iter[T]{fetch: fetch, cursor: cursor, idx: -1}
}

// Next advances to the next item, fetching the next page when needed. It returns false at the end or on an error.
func (it *Iter[T]) Next(ctx context.Context) bool {
	for {
		if it.err != nil {
			return false
		}
		if it.idx+1 < len(it.items) {
			it.idx++
			it.cur = it.items[it.idx]
			return true
		}
		if it.last {
			return false
		}
		p, err := it.fetch(ctx, it.cursor)
		if err != nil {
			it.err = err
			return false
		}
		it.items, it.idx, it.cursor = p.Items, -1, p.NextCursor
		it.last = p.NextCursor == ""
	}
}

// Current returns the item Next moved to.
func (it *Iter[T]) Current() T { return it.cur }

// Err returns the error that stopped the iteration, if any.
func (it *Iter[T]) Err() error { return it.err }

// MessagesService sends and reads messages.
type MessagesService struct{ c *Client }

func newIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("webhookadmin-go-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Send sends a message to every active endpoint of the consumer that accepts the event type.
// Pass WithIdempotencyKey to choose the key; otherwise one is generated and reused across retries.
// Requires the messages:send scope.
func (s *MessagesService) Send(ctx context.Context, params SendMessageParams, opts ...RequestOption) (*SendMessageResponse, error) {
	rc := s.c.config(opts)
	key := rc.idempotencyKey
	if key == "" {
		key = newIdempotencyKey()
	}
	var out SendMessageResponse
	err := s.c.do(ctx, request{method: "POST", path: "/v1/messages", body: params, headers: map[string]string{"Idempotency-Key": key}, idempotent: true}, &out, rc)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *MessagesService) page(ctx context.Context, p MessageListParams, cursor string, rc requestConfig) (*Page[MessageSummary], error) {
	q := url.Values{"status": {string(p.Status)}, "event_type": {p.EventType}, "q": {p.Q}, "since": {sinceString(p.Since)}, "limit": {limitString(p.Limit)}, "cursor": {cursor}}
	var out Page[MessageSummary]
	if err := s.c.do(ctx, request{method: "GET", path: "/v1/messages", query: q, idempotent: true}, &out, rc); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns one page of messages, newest first. Requires logs:read.
func (s *MessagesService) List(ctx context.Context, params *MessageListParams, opts ...RequestOption) (*Page[MessageSummary], error) {
	p := derefOr(params)
	return s.page(ctx, p, p.Cursor, s.c.config(opts))
}

// ListAll walks every message across pages, starting at params.Cursor. Requires logs:read.
func (s *MessagesService) ListAll(params *MessageListParams, opts ...RequestOption) *Iter[MessageSummary] {
	p := derefOr(params)
	rc := s.c.config(opts)
	return newIter(p.Cursor, func(ctx context.Context, cursor string) (*Page[MessageSummary], error) {
		return s.page(ctx, p, cursor, rc)
	})
}

// Get returns a message with its deliveries and attempts. Requires logs:read.
func (s *MessagesService) Get(ctx context.Context, messageID string, opts ...RequestOption) (*Message, error) {
	var out Message
	if err := s.c.do(ctx, request{method: "GET", path: "/v1/messages/" + esc(messageID), idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeliveriesService retries deliveries.
type DeliveriesService struct{ c *Client }

// Retry sends a finished delivery again. A delivery still being sent returns ErrConflict (Code "conflict").
// If the endpoint is disabled, it returns ErrConflict with Code "endpoint_disabled" and nothing is sent: fix the
// endpoint, set Status to EndpointActive with Endpoints.Update, then retry. A paused endpoint holds the retry until
// it is resumed. A deleted endpoint returns ErrNotFound. Requires messages:retry.
func (s *DeliveriesService) Retry(ctx context.Context, deliveryID string, opts ...RequestOption) error {
	return s.c.do(ctx, request{method: "POST", path: "/v1/deliveries/" + esc(deliveryID) + "/retry"}, nil, s.c.config(opts))
}

// ConsumersService manages consumers (your customers).
type ConsumersService struct{ c *Client }

// Create creates a consumer. A duplicate ExternalID returns ErrConflict. Requires consumers:write.
func (s *ConsumersService) Create(ctx context.Context, params ConsumerCreateParams, opts ...RequestOption) (*Consumer, error) {
	var out Consumer
	if err := s.c.do(ctx, request{method: "POST", path: "/v1/consumers", body: params}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete deletes a consumer with its endpoints and the sources that deliver to it. Portal links stop working and
// deliveries not yet sent are not sent. A missing consumer returns ErrNotFound. Requires consumers:write.
func (s *ConsumersService) Delete(ctx context.Context, consumerID string, opts ...RequestOption) error {
	return s.c.do(ctx, request{method: "DELETE", path: "/v1/consumers/" + esc(consumerID), idempotent: true}, nil, s.c.config(opts))
}

func (s *ConsumersService) page(ctx context.Context, p ConsumerListParams, cursor string, rc requestConfig) (*Page[Consumer], error) {
	q := url.Values{"limit": {limitString(p.Limit)}, "cursor": {cursor}}
	var out Page[Consumer]
	if err := s.c.do(ctx, request{method: "GET", path: "/v1/consumers", query: q, idempotent: true}, &out, rc); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns one page of consumers. Requires logs:read.
func (s *ConsumersService) List(ctx context.Context, params *ConsumerListParams, opts ...RequestOption) (*Page[Consumer], error) {
	p := derefOr(params)
	return s.page(ctx, p, p.Cursor, s.c.config(opts))
}

// ListAll walks every consumer across pages. Requires logs:read.
func (s *ConsumersService) ListAll(params *ConsumerListParams, opts ...RequestOption) *Iter[Consumer] {
	p := derefOr(params)
	rc := s.c.config(opts)
	return newIter(p.Cursor, func(ctx context.Context, cursor string) (*Page[Consumer], error) {
		return s.page(ctx, p, cursor, rc)
	})
}

// EndpointsService manages endpoints.
type EndpointsService struct{ c *Client }

func epPath(id, rest string) string { return "/v1/endpoints/" + esc(id) + rest }

// Create creates an endpoint. The signing secret is returned only here. Set Type to EndpointPolling (and no URL)
// for an endpoint the receiver polls; then create a poller token with CreatePollerToken. Requires endpoints:write.
func (s *EndpointsService) Create(ctx context.Context, params EndpointCreateParams, opts ...RequestOption) (*EndpointWithSecret, error) {
	var out EndpointWithSecret
	if err := s.c.do(ctx, request{method: "POST", path: "/v1/endpoints", body: params}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns every endpoint, optionally for one consumer, in one page. Requires logs:read.
func (s *EndpointsService) List(ctx context.Context, params *EndpointListParams, opts ...RequestOption) (*Page[Endpoint], error) {
	p := derefOr(params)
	var out Page[Endpoint]
	if err := s.c.do(ctx, request{method: "GET", path: "/v1/endpoints", query: url.Values{"consumer_id": {p.ConsumerID}}, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns an endpoint with its latest attempts. Requires logs:read.
func (s *EndpointsService) Get(ctx context.Context, endpointID string, opts ...RequestOption) (*EndpointDetail, error) {
	var out EndpointDetail
	if err := s.c.do(ctx, request{method: "GET", path: epPath(endpointID, ""), idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update updates an endpoint. Set Status to EndpointActive to resume a paused or disabled endpoint.
// Requires endpoints:write.
func (s *EndpointsService) Update(ctx context.Context, endpointID string, params EndpointUpdateParams, opts ...RequestOption) (*Endpoint, error) {
	var out Endpoint
	if err := s.c.do(ctx, request{method: "PATCH", path: epPath(endpointID, ""), body: params, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete deletes an endpoint. Requires endpoints:write.
func (s *EndpointsService) Delete(ctx context.Context, endpointID string, opts ...RequestOption) error {
	return s.c.do(ctx, request{method: "DELETE", path: epPath(endpointID, ""), idempotent: true}, nil, s.c.config(opts))
}

// RotateSecret issues a new signing secret. The previous one keeps signing for 24 hours. Requires endpoints:write.
func (s *EndpointsService) RotateSecret(ctx context.Context, endpointID string, opts ...RequestOption) (*RotateSecretResponse, error) {
	var out RotateSecretResponse
	if err := s.c.do(ctx, request{method: "POST", path: epPath(endpointID, "/rotate-secret")}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Recover resends every delivery to this endpoint that ended failed, for messages created at or after Since,
// as the same messages (same webhook-id). It runs in the background; check it with Recovery.
// The endpoint must be active: a disabled endpoint returns ErrConflict with Code "endpoint_disabled", a paused one
// ErrConflict with Code "conflict". Requires messages:retry.
func (s *EndpointsService) Recover(ctx context.Context, endpointID string, params RecoverParams, opts ...RequestOption) (*Recovery, error) {
	var out Recovery
	if err := s.c.do(ctx, request{method: "POST", path: epPath(endpointID, "/recover"), body: params}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Recovery returns the progress of a bulk resend. Requires logs:read.
func (s *EndpointsService) Recovery(ctx context.Context, endpointID, recoveryID string, opts ...RequestOption) (*Recovery, error) {
	var out Recovery
	if err := s.c.do(ctx, request{method: "GET", path: epPath(endpointID, "/recoveries/"+esc(recoveryID)), idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendTest sends a test message to this endpoint only. Not counted in usage. A disabled endpoint returns ErrConflict
// with Code "endpoint_disabled". Requires messages:send.
func (s *EndpointsService) SendTest(ctx context.Context, endpointID string, params *EndpointSendTestParams, opts ...RequestOption) (*SendTestResponse, error) {
	var out SendTestResponse
	p := derefOr(params)
	if err := s.c.do(ctx, request{method: "POST", path: epPath(endpointID, "/test"), body: p}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// TestDestination sends one test message to a destination other than a webhook right away and returns the result.
// Nothing is recorded. Counts toward the daily limit of test sends. Not retried. Pro plan and above. Requires endpoints:write.
func (s *EndpointsService) TestDestination(ctx context.Context, params DestinationTestParams, opts ...RequestOption) (*DestinationTestResult, error) {
	var out DestinationTestResult
	if err := s.c.do(ctx, request{method: "POST", path: "/v1/destinations/test", body: params}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetTransformation returns the endpoint's transformation. Returns ErrNotFound when there is none. Requires logs:read.
func (s *EndpointsService) GetTransformation(ctx context.Context, endpointID string, opts ...RequestOption) (*Transformation, error) {
	var out Transformation
	if err := s.c.do(ctx, request{method: "GET", path: epPath(endpointID, "/transformation"), idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetTransformation saves the endpoint's transformation, which runs right before every attempt.
// The signature covers the transformed body. Fields that are not set keep their current values.
// Pro plan and above (Enabled: Bool(false) alone works on any plan). Requires endpoints:write.
func (s *EndpointsService) SetTransformation(ctx context.Context, endpointID string, params TransformationSetParams, opts ...RequestOption) (*Transformation, error) {
	var out Transformation
	if err := s.c.do(ctx, request{method: "PUT", path: epPath(endpointID, "/transformation"), body: params, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteTransformation removes the endpoint's transformation. Requires endpoints:write.
func (s *EndpointsService) DeleteTransformation(ctx context.Context, endpointID string, opts ...RequestOption) error {
	return s.c.do(ctx, request{method: "DELETE", path: epPath(endpointID, "/transformation"), idempotent: true}, nil, s.c.config(opts))
}

// TestTransformation runs a transformation against a sample payload and returns the result.
// Nothing is sent or saved. Requires endpoints:write.
func (s *EndpointsService) TestTransformation(ctx context.Context, endpointID string, params *TransformationTestParams, opts ...RequestOption) (*TransformationTestResult, error) {
	var out TransformationTestResult
	p := derefOr(params)
	if err := s.c.do(ctx, request{method: "POST", path: epPath(endpointID, "/transformation/test"), body: p, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPollerTokens lists the poller tokens of a polling endpoint. Requires logs:read.
func (s *EndpointsService) ListPollerTokens(ctx context.Context, endpointID string, opts ...RequestOption) (*PollerTokenList, error) {
	var out PollerTokenList
	if err := s.c.do(ctx, request{method: "GET", path: epPath(endpointID, "/poller-tokens"), idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePollerToken creates a poller token (sk_poll_…) for a polling endpoint. The token is returned only here;
// it can only read this endpoint. Up to 5 per endpoint. Starter plan and above. Requires endpoints:write.
func (s *EndpointsService) CreatePollerToken(ctx context.Context, endpointID string, params *PollerTokenCreateParams, opts ...RequestOption) (*PollerTokenCreated, error) {
	var out PollerTokenCreated
	p := derefOr(params)
	if err := s.c.do(ctx, request{method: "POST", path: epPath(endpointID, "/poller-tokens"), body: p}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePollerToken deletes a poller token. It stops working right away. Requires endpoints:write.
func (s *EndpointsService) DeletePollerToken(ctx context.Context, endpointID, tokenID string, opts ...RequestOption) error {
	return s.c.do(ctx, request{method: "DELETE", path: epPath(endpointID, "/poller-tokens/"+esc(tokenID)), idempotent: true}, nil, s.c.config(opts))
}

// EventTypesService registers event types.
type EventTypesService struct{ c *Client }

// List returns the event types of the key's environment, by name (not paged). Any API key of the environment can call it.
func (s *EventTypesService) List(ctx context.Context, opts ...RequestOption) (*EventTypeList, error) {
	var out EventTypeList
	if err := s.c.do(ctx, request{method: "GET", path: "/v1/event-types", idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Create registers an event type. A duplicate name returns ErrConflict. Requires endpoints:write.
func (s *EventTypesService) Create(ctx context.Context, params EventTypeCreateParams, opts ...RequestOption) (*EventType, error) {
	var out EventType
	if err := s.c.do(ctx, request{method: "POST", path: "/v1/event-types", body: params}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Ensure registers the event types that do not exist yet, and leaves existing ones unchanged (their description
// included). Safe to call on every deploy. Requires endpoints:write.
func (s *EventTypesService) Ensure(ctx context.Context, types []EventTypeCreateParams, opts ...RequestOption) (*EnsureEventTypesResult, error) {
	var wanted []EventTypeCreateParams
	seen := map[string]bool{}
	for _, t := range types {
		if !seen[t.Name] {
			seen[t.Name] = true
			wanted = append(wanted, t)
		}
	}
	list, err := s.List(ctx, opts...)
	if err != nil {
		return nil, err
	}
	known := map[string]EventType{}
	for _, t := range list.Items {
		known[t.Name] = t
	}
	res := &EnsureEventTypesResult{Created: []EventType{}, Existing: []EventType{}}
	raced := map[string]bool{}
	for _, p := range wanted {
		if have, ok := known[p.Name]; ok {
			res.Existing = append(res.Existing, have)
			continue
		}
		t, err := s.Create(ctx, p, opts...)
		if err != nil {
			// registered by another caller after the list was read
			if errors.Is(err, ErrConflict) {
				raced[p.Name] = true
				continue
			}
			return nil, err
		}
		res.Created = append(res.Created, *t)
	}
	if len(raced) > 0 {
		again, err := s.List(ctx, opts...)
		if err != nil {
			return nil, err
		}
		for _, t := range again.Items {
			if raced[t.Name] {
				res.Existing = append(res.Existing, t)
			}
		}
	}
	return res, nil
}

// SourcesService manages sources: URLs that receive webhooks from other services (Stripe, GitHub and so on),
// verify their signatures and turn them into messages of a consumer.
type SourcesService struct{ c *Client }

func srcPath(id, rest string) string { return "/v1/sources/" + esc(id) + rest }

// Create creates a source. Register its URL (Source.URL) with the other service.
// Pass Secret to verify signatures (or set it later with SetSecret). Returns ErrPlanLimit when the plan's
// number of sources is reached. Requires endpoints:write.
func (s *SourcesService) Create(ctx context.Context, params SourceCreateParams, opts ...RequestOption) (*Source, error) {
	var out Source
	if err := s.c.do(ctx, request{method: "POST", path: "/v1/sources", body: params}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *SourcesService) page(ctx context.Context, p SourceListParams, cursor string, rc requestConfig) (*Page[Source], error) {
	q := url.Values{"limit": {limitString(p.Limit)}, "cursor": {cursor}}
	var out Page[Source]
	if err := s.c.do(ctx, request{method: "GET", path: "/v1/sources", query: q, idempotent: true}, &out, rc); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns one page of sources, in the order they were created. Requires logs:read.
func (s *SourcesService) List(ctx context.Context, params *SourceListParams, opts ...RequestOption) (*Page[Source], error) {
	p := derefOr(params)
	return s.page(ctx, p, p.Cursor, s.c.config(opts))
}

// ListAll walks every source across pages. Requires logs:read.
func (s *SourcesService) ListAll(params *SourceListParams, opts ...RequestOption) *Iter[Source] {
	p := derefOr(params)
	rc := s.c.config(opts)
	return newIter(p.Cursor, func(ctx context.Context, cursor string) (*Page[Source], error) {
		return s.page(ctx, p, cursor, rc)
	})
}

// Get returns a source. Requires logs:read.
func (s *SourcesService) Get(ctx context.Context, sourceID string, opts ...RequestOption) (*Source, error) {
	var out Source
	if err := s.c.do(ctx, request{method: "GET", path: srcPath(sourceID, ""), idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Update updates a source. Changing Provider without Config resets the config to its defaults.
// The signing secret is changed with SetSecret. Requires endpoints:write.
func (s *SourcesService) Update(ctx context.Context, sourceID string, params SourceUpdateParams, opts ...RequestOption) (*Source, error) {
	var out Source
	if err := s.c.do(ctx, request{method: "PATCH", path: srcPath(sourceID, ""), body: params, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete deletes a source and the requests it received. Its URL stops working. Requires endpoints:write.
func (s *SourcesService) Delete(ctx context.Context, sourceID string, opts ...RequestOption) error {
	return s.c.do(ctx, request{method: "DELETE", path: srcPath(sourceID, ""), idempotent: true}, nil, s.c.config(opts))
}

// SetSecret sets the secret that verifies the signatures the source receives (whsec_… for SourceStandard).
// The previous secret is still accepted for 24 hours. Requires endpoints:write.
func (s *SourcesService) SetSecret(ctx context.Context, sourceID, secret string, opts ...RequestOption) (*Source, error) {
	return s.putSecret(ctx, sourceID, &secret, opts)
}

// RemoveSecret removes the signing secret of the source. Requires endpoints:write.
func (s *SourcesService) RemoveSecret(ctx context.Context, sourceID string, opts ...RequestOption) (*Source, error) {
	return s.putSecret(ctx, sourceID, nil, opts)
}

func (s *SourcesService) putSecret(ctx context.Context, sourceID string, secret *string, opts []RequestOption) (*Source, error) {
	var out Source
	body := map[string]*string{"secret": secret}
	if err := s.c.do(ctx, request{method: "PUT", path: srcPath(sourceID, "/secret"), body: body, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *SourcesService) requestsPage(ctx context.Context, sourceID string, p SourceRequestListParams, cursor string, rc requestConfig) (*Page[SourceRequest], error) {
	q := url.Values{"limit": {limitString(p.Limit)}, "status": {string(p.Status)}, "cursor": {cursor}}
	var out Page[SourceRequest]
	if err := s.c.do(ctx, request{method: "GET", path: srcPath(sourceID, "/requests"), query: q, idempotent: true}, &out, rc); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListRequests returns one page of the requests the source received, newest first, within your plan's retention.
// Set Status to list only accepted, duplicate, rejected or held ones. Requires logs:read.
func (s *SourcesService) ListRequests(ctx context.Context, sourceID string, params *SourceRequestListParams, opts ...RequestOption) (*Page[SourceRequest], error) {
	p := derefOr(params)
	return s.requestsPage(ctx, sourceID, p, p.Cursor, s.c.config(opts))
}

// ListAllRequests walks every request the source received across pages, newest first. Requires logs:read.
func (s *SourcesService) ListAllRequests(sourceID string, params *SourceRequestListParams, opts ...RequestOption) *Iter[SourceRequest] {
	p := derefOr(params)
	rc := s.c.config(opts)
	return newIter(p.Cursor, func(ctx context.Context, cursor string) (*Page[SourceRequest], error) {
		return s.requestsPage(ctx, sourceID, p, cursor, rc)
	})
}

// ReprocessRequest processes a rejected request again (SourceRequest.Reprocessable) with the source's current secret
// and settings, such as after fixing a wrong secret, and returns the request with its new status. A request that was
// not rejected, or whose body was not kept, returns ErrConflict. Not retried on a 5xx. Requires messages:send.
func (s *SourcesService) ReprocessRequest(ctx context.Context, sourceID, requestID string, opts ...RequestOption) (*SourceRequest, error) {
	var out SourceRequest
	if err := s.c.do(ctx, request{method: "POST", path: srcPath(sourceID, "/requests/"+esc(requestID)+"/reprocess")}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

// PortalService creates consumer portal links.
type PortalService struct{ c *Client }

// CreateLink creates a 15-minute link to the consumer portal, where your customer manages their own endpoints.
// Starter plan or above. Requires endpoints:write.
func (s *PortalService) CreateLink(ctx context.Context, consumerID string, params *PortalLinkParams, opts ...RequestOption) (*PortalLink, error) {
	var out PortalLink
	p := derefOr(params)
	if err := s.c.do(ctx, request{method: "POST", path: "/v1/consumers/" + esc(consumerID) + "/portal", body: p, idempotent: true}, &out, s.c.config(opts)); err != nil {
		return nil, err
	}
	return &out, nil
}

func derefOr[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
