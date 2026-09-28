package webhookadmin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Verifying webhooks on the receiving side (Standard Webhooks):
//   webhook-id / webhook-timestamp (seconds) / webhook-signature: "v1,<base64>" (several, space-separated, during a rotation)
// The signed content is "{id}.{timestamp}.{body}"; the key is the base64-decoded part after "whsec_".

const secretPrefix = "whsec_"

// DefaultTolerance is the allowed clock difference, both ways.
const DefaultTolerance = 5 * time.Minute

const maxSafeInteger = 1<<53 - 1

var digits = regexp.MustCompile(`^[0-9]+$`)

// WebhookEvent is the body of a webhook sent by Webhook Admin.
type WebhookEvent struct {
	Type string `json:"type"`
	// Timestamp is ISO 8601.
	Timestamp string `json:"timestamp"`
	// Data is the payload of POST /v1/messages. Unmarshal it into your own type.
	Data json.RawMessage `json:"data"`
	// Raw is the whole verified body.
	Raw json.RawMessage `json:"-"`
}

// Webhook verifies webhooks sent by Webhook Admin.
type Webhook struct {
	key       []byte
	tolerance time.Duration
	now       func() time.Time
}

// WebhookOption configures a [Webhook].
type WebhookOption func(*Webhook)

// WithTolerance sets the allowed clock difference, both ways. Default 5 minutes.
func WithTolerance(d time.Duration) WebhookOption { return func(w *Webhook) { w.tolerance = d } }

func decodeBase64(s string) ([]byte, error) {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return base64.StdEncoding.DecodeString(s)
}

// NewWebhook creates a verifier. secret is the endpoint's signing secret (whsec_…).
func NewWebhook(secret string, opts ...WebhookOption) (*Webhook, error) {
	if secret == "" {
		return nil, newError(KindInvalidArgument, "Webhook secret is required", nil)
	}
	key, err := decodeBase64(strings.TrimPrefix(secret, secretPrefix))
	if err != nil {
		return nil, newError(KindInvalidArgument, "Webhook secret is not valid base64 (expected whsec_…)", err)
	}
	w := &Webhook{key: key, tolerance: DefaultTolerance, now: time.Now}
	for _, o := range opts {
		o(w)
	}
	return w, nil
}

func headerValue(h http.Header, name string) string {
	if v := h.Get(name); v != "" {
		return v
	}
	// headers built with lowercase keys without Set (map literals)
	for k, vs := range h {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
}

func (w *Webhook) mac(msgID, timestamp string, payload []byte) []byte {
	m := hmac.New(sha256.New, w.key)
	m.Write([]byte(msgID + "." + timestamp + "."))
	m.Write(payload)
	return m.Sum(nil)
}

func verificationError(message string, cause error) *Error {
	return newError(KindWebhookVerification, message, cause)
}

// Verify checks the signature and timestamp of a webhook, then parses the body.
// payload is the raw request body, exactly as received. The error matches ErrWebhookVerification when
// verification fails.
func (w *Webhook) Verify(payload []byte, headers http.Header) (*WebhookEvent, error) {
	msgID := headerValue(headers, "webhook-id")
	timestamp := headerValue(headers, "webhook-timestamp")
	signature := headerValue(headers, "webhook-signature")
	if msgID == "" || timestamp == "" || signature == "" {
		return nil, verificationError("Missing required headers", nil)
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if !digits.MatchString(timestamp) || err != nil || ts > maxSafeInteger {
		return nil, verificationError("Invalid webhook-timestamp header", nil)
	}
	now := w.now().Unix()
	tol := int64(w.tolerance / time.Second)
	if now-ts > tol {
		return nil, verificationError("Message timestamp too old", nil)
	}
	if ts-now > tol {
		return nil, verificationError("Message timestamp too new", nil)
	}

	expected := w.mac(msgID, timestamp, payload)
	for _, part := range strings.Split(signature, " ") {
		version, sig, ok := strings.Cut(part, ",")
		if !ok || version != "v1" || sig == "" {
			continue
		}
		given, err := decodeBase64(sig)
		if err != nil {
			continue
		}
		if hmac.Equal(given, expected) {
			if !json.Valid(payload) {
				return nil, verificationError("Payload is not valid JSON", nil)
			}
			ev := &WebhookEvent{Raw: append(json.RawMessage(nil), payload...)}
			// a body that is not an event object still verifies; its fields stay empty and Raw has it
			_ = json.Unmarshal(payload, ev)
			return ev, nil
		}
	}
	return nil, verificationError("No matching signature found", nil)
}

// Sign computes the webhook-signature value ("v1,<base64>"). Useful for tests.
func (w *Webhook) Sign(msgID string, timestamp time.Time, payload []byte) string {
	return "v1," + base64.StdEncoding.EncodeToString(w.mac(msgID, strconv.FormatInt(timestamp.Unix(), 10), payload))
}
