// Package webhookadmin is the Go SDK for Webhook Admin (https://webhookadmin.com/docs/):
// send webhooks, manage endpoints and verify signatures.
package webhookadmin

import (
	"context"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public API.
	DefaultBaseURL = "https://api.webhookadmin.com"
	// DefaultTimeout is the time allowed for each attempt.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxRetries is the number of retries after the first attempt.
	DefaultMaxRetries = 2
	// APIKeyEnv is read when the API key argument is empty.
	APIKeyEnv = "WEBHOOK_ADMIN_API_KEY"
)

// Client for the Webhook Admin API (/v1). It is safe for concurrent use.
type Client struct {
	Messages   *MessagesService
	Consumers  *ConsumersService
	Endpoints  *EndpointsService
	Deliveries *DeliveriesService
	EventTypes *EventTypesService
	Portal     *PortalService

	apiKey     string
	baseURL    string
	timeout    time.Duration
	maxRetries int
	httpClient *http.Client

	// replaced in tests
	sleep  func(ctx context.Context, d time.Duration) error
	random func() float64
	now    func() time.Time
}

// Option configures a [Client].
type Option func(*Client)

// WithBaseURL sets the API origin. Default https://api.webhookadmin.com.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithTimeout sets the time allowed for each attempt. Default 30 seconds.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithMaxRetries sets the retries after the first attempt. Default 2. 0 disables retries.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithHTTPClient sets the HTTP client. Default: a new http.Client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// NewClient creates a client. apiKey is sk_live_… or sk_test_…; when empty, WEBHOOK_ADMIN_API_KEY is used.
func NewClient(apiKey string, opts ...Option) (*Client, error) {
	key := apiKey
	if key == "" {
		key = strings.TrimSpace(os.Getenv(APIKeyEnv))
	}
	if key == "" {
		return nil, newError(KindInvalidArgument, "Missing API key. Pass it to webhookadmin.NewClient(\"sk_…\") or set "+APIKeyEnv+".", nil)
	}
	c := &Client{
		apiKey:     key,
		baseURL:    DefaultBaseURL,
		timeout:    DefaultTimeout,
		maxRetries: DefaultMaxRetries,
		sleep:      sleepContext,
		random:     rand.Float64,
		now:        time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	c.Messages = &MessagesService{c}
	c.Consumers = &ConsumersService{c}
	c.Endpoints = &EndpointsService{c}
	c.Deliveries = &DeliveriesService{c}
	c.EventTypes = &EventTypesService{c}
	c.Portal = &PortalService{c}
	return c, nil
}

// BaseURL returns the API origin without a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL }

// RequestOption overrides client settings for one call.
type RequestOption func(*requestConfig)

type requestConfig struct {
	timeout        time.Duration
	maxRetries     int
	idempotencyKey string
}

// WithRequestTimeout sets the time allowed for each attempt of this call.
func WithRequestTimeout(d time.Duration) RequestOption {
	return func(r *requestConfig) { r.timeout = d }
}

// WithRequestMaxRetries sets the retries after the first attempt for this call.
func WithRequestMaxRetries(n int) RequestOption {
	return func(r *requestConfig) { r.maxRetries = n }
}

// WithIdempotencyKey sets the Idempotency-Key of Messages.Send (1 to 256 characters).
// The same key returns the same message for 24 hours. Generated when omitted, and reused across retries.
func WithIdempotencyKey(key string) RequestOption {
	return func(r *requestConfig) { r.idempotencyKey = key }
}

func (c *Client) config(opts []RequestOption) requestConfig {
	r := requestConfig{timeout: c.timeout, maxRetries: c.maxRetries}
	for _, o := range opts {
		o(&r)
	}
	return r
}

// String returns a pointer to s, for optional fields.
func String(s string) *string { return &s }

// Bool returns a pointer to b, for optional fields.
func Bool(b bool) *bool { return &b }
