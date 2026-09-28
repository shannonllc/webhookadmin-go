package webhookadmin

import (
	"encoding/json"
	"time"
)

// Shapes of /v1 requests and responses (docs/api.md). Times are Unix milliseconds.

// Page is one page of a list.
type Page[T any] struct {
	Items []T `json:"items"`
	// NextCursor is passed as Cursor to get the next page. Empty on the last page.
	NextCursor string `json:"next_cursor"`
}

// MessageStatus is pending, retrying, success, failed or cancelled.
// cancelled means every delivery of the message was cancelled by a transformation.
type MessageStatus string

// DeliveryStatus is pending, held, retrying, success, failed, cancelled or waiting. held means the endpoint is paused.
// cancelled means the endpoint's transformation set cancel: true (not a failure; not retried).
// waiting means the message is stored for a polling endpoint and has not been fetched yet (the message counts as pending).
type DeliveryStatus string

// EndpointStatus is active, paused or disabled.
type EndpointStatus string

// RecoveryStatus is running, done or stopped.
type RecoveryStatus string

const (
	MessagePending   MessageStatus = "pending"
	MessageRetrying  MessageStatus = "retrying"
	MessageSuccess   MessageStatus = "success"
	MessageFailed    MessageStatus = "failed"
	MessageCancelled MessageStatus = "cancelled"

	DeliveryPending   DeliveryStatus = "pending"
	DeliveryHeld      DeliveryStatus = "held"
	DeliveryRetrying  DeliveryStatus = "retrying"
	DeliverySuccess   DeliveryStatus = "success"
	DeliveryFailed    DeliveryStatus = "failed"
	DeliveryCancelled DeliveryStatus = "cancelled"
	DeliveryWaiting   DeliveryStatus = "waiting"

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
	// Via is "direct" or "relay" for webhooks, "poll" for a polling endpoint (At is when the receiver fetched the message),
	// or the destination type (such as "sqs") for other destinations.
	Via string `json:"via"`
	// Ref is the ID returned by a destination other than a webhook (SQS MessageId, object key and so on), or nil.
	Ref *string `json:"ref"`
	// SourceIP is the static IP used when sent through the relay. nil for direct sends.
	SourceIP *string `json:"source_ip"`
	// Transform is "changed" when the endpoint's transformation changed the request, "error" when it failed
	// (nothing was sent), or nil.
	Transform *string `json:"transform"`
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
	// TransformationsParams is a JSON object (up to 4 KB as JSON) passed to the endpoints' transformations
	// as webhook.transformationsParams. nil sends nothing.
	TransformationsParams map[string]any `json:"transformations_params,omitempty"`
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
	// Since is Unix milliseconds: only messages created at or after it. 0 leaves it out (with Q, Status or EventType,
	// the last 7 days are searched).
	Since int64
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
	ID         string       `json:"id"`
	ConsumerID string       `json:"consumer_id"`
	Type       EndpointType `json:"type"`
	// URL is empty for a polling endpoint. For other destinations, a readable form such as "sqs://region/account/queue".
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
	// Ordering is the delivery order. Default OrderingNone.
	Ordering Ordering `json:"ordering"`
	// BodyFormat is the body sent to the endpoint. Default BodyFormatStandard.
	BodyFormat BodyFormat `json:"body_format"`
	// TransformationUpdatedAt is Unix milliseconds when the transformation was last saved, or nil when there is none
	// or it is disabled.
	TransformationUpdatedAt *int64 `json:"transformation_updated_at"`
	// LastPolledAt is Unix milliseconds when the receiver last polled (recorded at most once a minute), or nil.
	// Always nil for an http endpoint.
	LastPolledAt *int64 `json:"last_polled_at"`
	// Destination holds the settings of a destination other than a webhook and "credentials_hint"
	// (a masked hint of the stored credentials). nil for webhooks.
	Destination Destination `json:"destination"`
}

// EndpointType is "http" (a webhook), "polling" (no URL; the receiver fetches messages with a poller token, see [Poller])
// or a destination other than a webhook (Pro plan and above).
type EndpointType string

const (
	EndpointHTTP    EndpointType = "http"
	EndpointPolling EndpointType = "polling"

	DestinationSQS         EndpointType = "sqs"
	DestinationEventBridge EndpointType = "eventbridge"
	DestinationPubSub      EndpointType = "pubsub"
	DestinationS3          EndpointType = "s3"
	DestinationR2          EndpointType = "r2"
	DestinationGCS         EndpointType = "gcs"
	DestinationAzureBlob   EndpointType = "azure_blob"
	DestinationServiceBus  EndpointType = "servicebus"
	DestinationKafka       EndpointType = "kafka"
	DestinationRabbitMQ    EndpointType = "rabbitmq"
)

// Ordering is the delivery order of an endpoint.
type Ordering string

const (
	// OrderingNone sends deliveries in parallel.
	OrderingNone Ordering = "none"
	// OrderingFIFO sends deliveries one at a time in the order the messages were accepted: the next delivery waits
	// until the previous one succeeds or ends as failed (retries included).
	OrderingFIFO Ordering = "fifo"
)

// BodyFormat is the body sent to an endpoint. The signature covers the body that is sent.
type BodyFormat string

const (
	// BodyFormatStandard sends the Standard Webhooks body {"type", "timestamp", "data"}.
	BodyFormatStandard BodyFormat = "standard"
	// BodyFormatRaw sends the message data itself (as Svix does).
	BodyFormatRaw BodyFormat = "raw"
)

// Destination is the settings of a destination as returned by the API, with "credentials_hint".
type Destination map[string]string

// DestinationParams is the settings of a destination when creating or updating, such as
// {"region": ..., "account_id": ..., "queue_name": ..., "credentials": {"access_key_id": ..., "secret_access_key": ...}}.
// Credentials are write-only. On update, omit "credentials" to keep the current ones
// (changing the host of a kafka or rabbitmq URL requires new credentials).
type DestinationParams map[string]any

// DestinationTestParams: pass EndpointID to use the saved settings and credentials, or Type and Destination
// to try settings before saving.
type DestinationTestParams struct {
	EndpointID  string            `json:"endpoint_id,omitempty"`
	Type        EndpointType      `json:"type,omitempty"`
	Destination DestinationParams `json:"destination,omitempty"`
	// FixedIP sends through the static-IP relay. nil uses the endpoint's setting.
	FixedIP *bool `json:"fixed_ip,omitempty"`
}

// DestinationTestResult is the result of sending one test message right away. Nothing is recorded.
type DestinationTestResult struct {
	OK             bool         `json:"ok"`
	Via            EndpointType `json:"via"`
	ResponseStatus *int         `json:"response_status"`
	DurationMS     int64        `json:"duration_ms"`
	Error          *string      `json:"error"`
	// ResponseHead is the first 1024 bytes of the response when it failed.
	ResponseHead *string `json:"response_head"`
	Ref          *string `json:"ref"`
}

// EndpointWithSecret is returned by Create. Secret (whsec_…, or the imported one) is returned only then.
type EndpointWithSecret struct {
	Endpoint
	Secret string `json:"secret"`
	// PollerURL is the URL the receiver polls (…/v1/poller/{endpoint_id}) for a polling endpoint, or nil.
	PollerURL *string `json:"poller_url"`
}

type EndpointDetail struct {
	Endpoint
	// Recent is the latest attempts to this endpoint this month (up to 20).
	Recent []Attempt `json:"recent"`
	// RetryingCount is the number of deliveries waiting for a retry.
	RetryingCount int `json:"retrying_count"`
	// WaitingCount is, with OrderingFIFO, the number of deliveries waiting behind the one being sent. 0 otherwise.
	WaitingCount int `json:"waiting_count"`
	// Recovery is the latest bulk resend, or nil.
	Recovery *Recovery `json:"recovery"`
	// PollerURL is the URL the receiver polls (…/v1/poller/{endpoint_id}) for a polling endpoint, or nil.
	PollerURL *string `json:"poller_url"`
}

// EndpointCreateParams: omitted fields use the defaults (all event types, default retry policy, no compat signature).
type EndpointCreateParams struct {
	ConsumerID string `json:"consumer_id"`
	// URL is https (port 443 or 8443). Required for webhooks (the default type); leave it empty for a polling endpoint.
	URL string `json:"url,omitempty"`
	// Type defaults to EndpointHTTP. EndpointPolling has no URL. Other types take Destination instead of URL.
	// It cannot be changed later.
	Type        EndpointType      `json:"type,omitempty"`
	Destination DestinationParams `json:"destination,omitempty"`
	// EventTypes to receive. nil receives all.
	EventTypes      []string         `json:"event_types,omitempty"`
	FixedIP         bool             `json:"fixed_ip,omitempty"`
	Description     string           `json:"description,omitempty"`
	Retry           *RetryPolicy     `json:"retry,omitempty"`
	CompatSignature *CompatSignature `json:"compat_signature,omitempty"`
	// Ordering defaults to OrderingNone.
	Ordering Ordering `json:"ordering,omitempty"`
	// BodyFormat defaults to BodyFormatStandard.
	BodyFormat BodyFormat `json:"body_format,omitempty"`
	// Secret is an existing signing secret to keep, such as one moved from Svix: "whsec_" and standard base64 of
	// 24 to 75 bytes ("whsec_" may be omitted). Empty generates one. It cannot be set on update (use RotateSecret).
	Secret string `json:"secret,omitempty"`
}

// EndpointUpdateParams: only the fields that are set are sent.
type EndpointUpdateParams struct {
	URL *string
	// EventTypes: Null[[]string]() receives all event types.
	EventTypes Nullable[[]string]
	// Status "active" also re-enables a disabled endpoint and sends messages held while paused.
	Status      *EndpointStatus
	Description *string
	// FixedIP sends through the static-IP relay (paid plans).
	FixedIP *bool
	// Retry: Null[RetryPolicy]() goes back to the default policy.
	Retry Nullable[RetryPolicy]
	// CompatSignature: Null[CompatSignature]() removes it.
	CompatSignature Nullable[CompatSignature]
	// Destination changes the settings of a destination other than a webhook.
	Destination DestinationParams
	// Ordering: switching to OrderingNone sends the waiting deliveries right away, in parallel.
	Ordering   *Ordering
	BodyFormat *BodyFormat
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
	if p.FixedIP != nil {
		m["fixed_ip"] = *p.FixedIP
	}
	if p.Retry.set {
		m["retry"] = p.Retry.jsonValue()
	}
	if p.CompatSignature.set {
		m["compat_signature"] = p.CompatSignature.jsonValue()
	}
	if p.Destination != nil {
		m["destination"] = p.Destination
	}
	if p.Ordering != nil {
		m["ordering"] = *p.Ordering
	}
	if p.BodyFormat != nil {
		m["body_format"] = *p.BodyFormat
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

// Transformation is an endpoint's transformation: JavaScript that defines handler(webhook) (Pro plan and above).
type Transformation struct {
	EndpointID string `json:"endpoint_id"`
	Code       string `json:"code"`
	// UpdatedAt is Unix milliseconds.
	UpdatedAt int64 `json:"updated_at"`
	// Enabled false keeps the code but does not apply it.
	Enabled bool `json:"enabled"`
	// Variables are read-only values for the code, as webhook.env. nil when none.
	Variables map[string]string `json:"variables"`
}

// TransformationSetParams: only the fields that are set are sent; the others keep their current values.
type TransformationSetParams struct {
	// Code is JavaScript defining function handler(webhook), up to 51,200 characters. Required when the endpoint has
	// no transformation yet; empty keeps the saved code. webhook is
	// { method, url, eventType, payload, headers, cancel, env, transformationsParams }; return it after changing payload,
	// headers, method (POST / PUT / PATCH) or the path and query of url, or set cancel: true.
	Code string
	// Enabled: Bool(false) keeps the code but stops applying it. A new transformation is enabled.
	Enabled *bool
	// Variables are up to 50 string values (names of 1 to 100 characters, up to 4 KB as JSON), read-only as webhook.env.
	// Null[map[string]string]() removes them.
	Variables Nullable[map[string]string]
}

// MarshalJSON sends only the fields that are set.
func (p TransformationSetParams) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if p.Code != "" {
		m["code"] = p.Code
	}
	if p.Enabled != nil {
		m["enabled"] = *p.Enabled
	}
	if p.Variables.set {
		m["variables"] = p.Variables.jsonValue()
	}
	return json.Marshal(m)
}

// TransformationTestParams: omitted fields use the defaults.
type TransformationTestParams struct {
	// Code to run. Empty uses the saved transformation.
	Code string
	// Payload is the sample data, any value that encodes to JSON. nil uses {"test": true, "endpoint_id": …}.
	Payload any
	// EventType defaults to "webhook.test".
	EventType string
	// Variables is webhook.env. Not set uses the saved variables; Null[map[string]string]() runs with none.
	Variables Nullable[map[string]string]
	// TransformationsParams is webhook.transformationsParams, as given in SendMessageParams.
	TransformationsParams map[string]any
}

// MarshalJSON sends only the fields that are set.
func (p TransformationTestParams) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if p.Code != "" {
		m["code"] = p.Code
	}
	if p.Payload != nil {
		m["payload"] = p.Payload
	}
	if p.EventType != "" {
		m["event_type"] = p.EventType
	}
	if p.Variables.set {
		m["variables"] = p.Variables.jsonValue()
	}
	if p.TransformationsParams != nil {
		m["transformations_params"] = p.TransformationsParams
	}
	return json.Marshal(m)
}

// TransformationResultKind is send, cancel or error.
type TransformationResultKind string

// TransformationErrorKind is why a transformation failed.
type TransformationErrorKind string

const (
	TransformationSend   TransformationResultKind = "send"
	TransformationCancel TransformationResultKind = "cancel"
	TransformationError  TransformationResultKind = "error"

	TransformationErrCodeTooLarge   TransformationErrorKind = "code_too_large"
	TransformationErrSyntax         TransformationErrorKind = "syntax"
	TransformationErrNoHandler      TransformationErrorKind = "no_handler"
	TransformationErrTimeout        TransformationErrorKind = "timeout"
	TransformationErrMemory         TransformationErrorKind = "memory"
	TransformationErrRuntime        TransformationErrorKind = "runtime"
	TransformationErrInvalidOutput  TransformationErrorKind = "invalid_output"
	TransformationErrOutputTooLarge TransformationErrorKind = "output_too_large"
	TransformationErrInternal       TransformationErrorKind = "internal"
)

type TransformationTestError struct {
	Kind    TransformationErrorKind `json:"kind"`
	Message string                  `json:"message"`
}

// TransformationTestResult is what the transformation would do. Nothing is sent.
// Method, URL, Headers, Payload and Changed are set when Result is "send"; Error when Result is "error".
type TransformationTestResult struct {
	Result TransformationResultKind `json:"result"`
	// Method is "POST", "PUT" or "PATCH".
	Method string `json:"method,omitempty"`
	URL    string `json:"url,omitempty"`
	// Headers the transformation adds.
	Headers map[string]string `json:"headers,omitempty"`
	// Payload is the body that would be sent (the whole JSON body).
	Payload json.RawMessage          `json:"payload,omitempty"`
	Changed bool                     `json:"changed,omitempty"`
	Error   *TransformationTestError `json:"error,omitempty"`
	// Logs holds console.log output.
	Logs []string `json:"logs"`
}

// PollerToken is a token the receiver uses to poll one polling endpoint. It can only read that endpoint.
type PollerToken struct {
	// ID is ptk_….
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	Name       string `json:"name"`
	// Prefix is the first characters of the token, such as "sk_poll_abcd…".
	Prefix    string `json:"prefix"`
	CreatedAt int64  `json:"created_at"`
	// LastUsedAt is recorded at most once a minute. nil when never used.
	LastUsedAt *int64 `json:"last_used_at"`
}

// PollerTokenCreated is returned by CreatePollerToken. Token (sk_poll_…) is returned only then.
type PollerTokenCreated struct {
	PollerToken
	Token string `json:"token"`
}

type PollerTokenCreateParams struct {
	// Name up to 100 characters.
	Name string `json:"name,omitempty"`
}

type PollerTokenList struct {
	Items []PollerToken `json:"items"`
}

// PolledMessage is a message fetched from a polling endpoint: the same ID, body and signature as a pushed webhook.
type PolledMessage struct {
	// ID is msg_…, also sent as webhook-id.
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	// Timestamp is ISO 8601.
	Timestamp string `json:"timestamp"`
	// Payload is the Standard Webhooks body, {"type", "timestamp", "data"}. With BodyFormatRaw, the message data itself.
	Payload json.RawMessage `json:"payload"`
	// Headers are webhook-id, webhook-timestamp and webhook-signature (signed with the endpoint's secret).
	Headers map[string]string `json:"headers"`
}

// PollPage is one page from GET /v1/poller/{endpoint_id}.
type PollPage struct {
	Data []PolledMessage `json:"data"`
	// Iterator is passed to the next call, which acknowledges this page; an older iterator returns the same messages again.
	// Save it after you have processed Data to resume from there.
	Iterator string `json:"iterator"`
	// Done is true when there is nothing more right now.
	Done bool `json:"done"`
}

type PollParams struct {
	// Iterator of the previous page. Empty starts at the beginning of your plan's retention.
	Iterator string
	// Limit is 1 to 250. Default 50.
	Limit int
	// EventTypes narrows the messages to these event types (within the endpoint's event types, up to 100).
	EventTypes []string
	// After starts from messages accepted at or after this time when Iterator is empty. The zero time leaves it out.
	After time.Time
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
