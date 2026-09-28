package webhookadmin

import (
	"encoding/json"
)

// Shapes of /v1 requests and responses (docs/api.md). Times are Unix milliseconds.

// Page is one page of a list.
type Page[T any] struct {
	Items []T `json:"items"`
	// NextCursor is passed as Cursor to get the next page. Empty on the last page.
	NextCursor string `json:"next_cursor"`
}

// MessageStatus is pending, retrying, success or failed.
type MessageStatus string

// DeliveryStatus is pending, held, retrying, success or failed. held means the endpoint is paused.
type DeliveryStatus string

// EndpointStatus is active, paused or disabled.
type EndpointStatus string

// RecoveryStatus is running, done or stopped.
type RecoveryStatus string

const (
	MessagePending  MessageStatus = "pending"
	MessageRetrying MessageStatus = "retrying"
	MessageSuccess  MessageStatus = "success"
	MessageFailed   MessageStatus = "failed"

	EndpointActive   EndpointStatus = "active"
	EndpointPaused   EndpointStatus = "paused"
	EndpointDisabled EndpointStatus = "disabled"
)

type ConsumerRef struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}

// MessageSummary is a message in a list.
type MessageSummary struct {
	ID        string        `json:"id"`
	EventType string        `json:"event_type"`
	Consumer  ConsumerRef   `json:"consumer"`
	CreatedAt int64         `json:"created_at"`
	Status    MessageStatus `json:"status"`
	Attempts  int           `json:"attempts"`
	// EndpointURL is the URL of the first endpoint, or nil when the consumer had no endpoint.
	EndpointURL *string `json:"endpoint_url"`
}

type Attempt struct {
	ID             string `json:"id"`
	At             int64  `json:"at"`
	ResponseStatus *int   `json:"response_status"`
	// ResponseHead is the first 1024 bytes of the response body.
	ResponseHead *string `json:"response_head"`
	DurationMS   int64   `json:"duration_ms"`
	Error        *string `json:"error"`
	// Via is "direct" or "relay".
	Via string `json:"via"`
	// SourceIP is the static IP used when sent through the relay. nil for direct sends.
	SourceIP *string `json:"source_ip"`
}

type Delivery struct {
	ID            string         `json:"id"`
	EndpointID    string         `json:"endpoint_id"`
	URL           string         `json:"url"`
	Status        DeliveryStatus `json:"status"`
	AttemptCount  int            `json:"attempt_count"`
	NextAttemptAt *int64         `json:"next_attempt_at"`
	Attempts      []Attempt      `json:"attempts"`
}

type Message struct {
	ID             string        `json:"id"`
	EventType      string        `json:"event_type"`
	Consumer       ConsumerRef   `json:"consumer"`
	CreatedAt      int64         `json:"created_at"`
	Status         MessageStatus `json:"status"`
	IdempotencyKey *string       `json:"idempotency_key"`
	// Payload as sent. "null" if it is no longer stored.
	Payload    json.RawMessage `json:"payload"`
	Deliveries []Delivery      `json:"deliveries"`
}

type SendMessageParams struct {
	// Consumer is your customer's ID (external_id). A consumer is created if it does not exist.
	Consumer string `json:"consumer"`
	// EventType such as "invoice.paid".
	EventType string `json:"event_type"`
	// Payload is any value that encodes to JSON, up to 1 MB. Delivered as "data".
	Payload any `json:"payload"`
}

type SendMessageResponse struct {
	ID string `json:"id"`
	// Deliveries is the number of endpoints the message is delivered to.
	Deliveries int `json:"deliveries"`
}

type MessageListParams struct {
	Status    MessageStatus
	EventType string
	// Q matches the message ID, event type, consumer external_id, or payload text (3+ characters).
	Q string
	// Limit is 1 to 100. Default 50.
	Limit  int
	Cursor string
}

type Consumer struct {
	ID            string `json:"id"`
	ExternalID    string `json:"external_id"`
	Name          string `json:"name"`
	CreatedAt     int64  `json:"created_at"`
	EndpointCount int    `json:"endpoint_count"`
}

type ConsumerCreateParams struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name,omitempty"`
}

type ConsumerListParams struct {
	// Limit is 1 to 100. Default 50.
	Limit  int
	Cursor string
}

// RetryPolicy of an endpoint. Default {Count: 7, Interval: "progressive"}: up to 8 attempts over about 28 hours.
// Count is 0, 1, 2, 3, 5, 7 or 12 (retries after the first send). Interval is "progressive", "1m", "5m", "30m",
// "1h" or "6h". Combinations whose last retry comes more than 3 days after the first send are rejected.
type RetryPolicy struct {
	Count    int    `json:"count"`
	Interval string `json:"interval"`
}

// CompatSignature is an extra HMAC-SHA256 signature header for receivers that still verify a custom signature.
// The key is the endpoint's signing secret string (whsec_…) as UTF-8.
type CompatSignature struct {
	// Header is a lowercase header name, such as "x-hub-signature-256".
	Header string `json:"header"`
	// Content is "body" (the body only, no replay protection) or "timestamp_body" ("{webhook-timestamp}.{body}").
	Content string `json:"content"`
	// Encoding is "hex" or "base64".
	Encoding string `json:"encoding"`
	// Prefix is prepended to the value, such as "sha256=". Printable ASCII without spaces, up to 32 characters.
	Prefix string `json:"prefix,omitempty"`
}

// Recovery is a bulk resend of the failed deliveries of one endpoint.
type Recovery struct {
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	Since      int64  `json:"since"`
	// Status "stopped" means the endpoint was paused, disabled or deleted before it finished.
	Status     RecoveryStatus `json:"status"`
	Total      int            `json:"total"`
	Queued     int            `json:"queued"`
	CreatedAt  int64          `json:"created_at"`
	FinishedAt *int64         `json:"finished_at"`
}

type RecoverParams struct {
	// Since is Unix milliseconds, within your plan's retention and not in the future.
	Since int64 `json:"since"`
}

type Endpoint struct {
	ID          string `json:"id"`
	ConsumerID  string `json:"consumer_id"`
	URL         string `json:"url"`
	Description string `json:"description"`
	// EventTypes sent to this endpoint. nil means all.
	EventTypes      []string         `json:"event_types"`
	FixedIP         bool             `json:"fixed_ip"`
	Status          EndpointStatus   `json:"status"`
	DisabledReason  *string          `json:"disabled_reason"`
	FailingSince    *int64           `json:"failing_since"`
	CreatedAt       int64            `json:"created_at"`
	Retry           RetryPolicy      `json:"retry"`
	CompatSignature *CompatSignature `json:"compat_signature"`
}

// EndpointWithSecret is returned by Create. Secret (whsec_…) is returned only then.
type EndpointWithSecret struct {
	Endpoint
	Secret string `json:"secret"`
}

type EndpointDetail struct {
	Endpoint
	// Recent is the latest attempts to this endpoint this month (up to 20).
	Recent []Attempt `json:"recent"`
	// RetryingCount is the number of deliveries waiting for a retry.
	RetryingCount int `json:"retrying_count"`
	// Recovery is the latest bulk resend, or nil.
	Recovery *Recovery `json:"recovery"`
}

// EndpointCreateParams: omitted fields use the defaults (all event types, default retry policy, no compat signature).
type EndpointCreateParams struct {
	ConsumerID string `json:"consumer_id"`
	// URL is https (port 443 or 8443).
	URL string `json:"url"`
	// EventTypes to receive. nil receives all.
	EventTypes      []string         `json:"event_types,omitempty"`
	FixedIP         bool             `json:"fixed_ip,omitempty"`
	Description     string           `json:"description,omitempty"`
	Retry           *RetryPolicy     `json:"retry,omitempty"`
	CompatSignature *CompatSignature `json:"compat_signature,omitempty"`
}

// EndpointUpdateParams: only the fields that are set are sent.
type EndpointUpdateParams struct {
	URL *string
	// EventTypes: Null[[]string]() receives all event types.
	EventTypes Nullable[[]string]
	// Status "active" also re-enables a disabled endpoint and sends messages held while paused.
	Status      *EndpointStatus
	Description *string
	// Retry: Null[RetryPolicy]() goes back to the default policy.
	Retry Nullable[RetryPolicy]
	// CompatSignature: Null[CompatSignature]() removes it.
	CompatSignature Nullable[CompatSignature]
}

// MarshalJSON sends only the fields that are set.
func (p EndpointUpdateParams) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if p.URL != nil {
		m["url"] = *p.URL
	}
	if p.EventTypes.set {
		m["event_types"] = p.EventTypes.jsonValue()
	}
	if p.Status != nil {
		m["status"] = *p.Status
	}
	if p.Description != nil {
		m["description"] = *p.Description
	}
	if p.Retry.set {
		m["retry"] = p.Retry.jsonValue()
	}
	if p.CompatSignature.set {
		m["compat_signature"] = p.CompatSignature.jsonValue()
	}
	return json.Marshal(m)
}

type EndpointListParams struct {
	ConsumerID string
}

type EndpointSendTestParams struct {
	// EventType defaults to "webhook.test".
	EventType string `json:"event_type,omitempty"`
}

type RotateSecretResponse struct {
	// Secret is the new signing secret. The previous one keeps signing for 24 hours.
	Secret string `json:"secret"`
}

type SendTestResponse struct {
	MessageID string `json:"message_id"`
}

type PortalLinkParams struct {
	// FrameOrigin is the origin of the page that embeds the portal in an iframe.
	FrameOrigin string `json:"frame_origin,omitempty"`
	// Locale is "ja" or "en".
	Locale string `json:"locale,omitempty"`
}

type PortalLink struct {
	// URL is a one-time URL of the consumer portal.
	URL string `json:"url"`
	// ExpiresAt is Unix milliseconds. The link is valid for 15 minutes.
	ExpiresAt int64 `json:"expires_at"`
}

type EventType struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// ArchivedAt is Unix milliseconds when archived, or nil.
	ArchivedAt *int64 `json:"archived_at"`
}

type EventTypeCreateParams struct {
	// Name: letters, digits and _ . : -, up to 100 characters.
	Name string `json:"name"`
	// Description up to 200 characters.
	Description string `json:"description,omitempty"`
}

type EventTypeList struct {
	// Items sorted by name.
	Items []EventType `json:"items"`
}

type EnsureEventTypesResult struct {
	// Created by this call.
	Created []EventType
	// Existing event types, as they are stored.
	Existing []EventType
}

// Nullable is an optional field that can also be sent as JSON null. The zero value is "not set".
type Nullable[T any] struct {
	value *T
	set   bool
}

// Value returns a Nullable that sends v.
func Value[T any](v T) Nullable[T] { return Nullable[T]{value: &v, set: true} }

// Null returns a Nullable that sends JSON null.
func Null[T any]() Nullable[T] { return Nullable[T]{set: true} }

// IsSet reports whether the field is sent.
func (n Nullable[T]) IsSet() bool { return n.set }

// IsNull reports whether the field is sent as null.
func (n Nullable[T]) IsNull() bool { return n.set && n.value == nil }

func (n Nullable[T]) jsonValue() any {
	if n.value == nil {
		return nil
	}
	return *n.value
}
