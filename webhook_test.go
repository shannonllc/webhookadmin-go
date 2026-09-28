package webhookadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// The vectors are signed by the Worker, the Node SDK and the official Standard Webhooks library,
// with results decided by the Node SDK (scripts/sdk-vectors.mts in the monorepo).
type vector struct {
	Name      string            `json:"name"`
	Secret    string            `json:"secret"`
	Payload   string            `json:"payload"`
	Headers   map[string]string `json:"headers"`
	Now       int64             `json:"now"`
	Tolerance *int64            `json:"tolerance"`
	Expect    json.RawMessage   `json:"expect"`
	Error     string            `json:"error"`
}

func loadVectors(t *testing.T) []vector {
	t.Helper()
	b, err := os.ReadFile("testdata/webhook_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Vectors []vector }
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Vectors) < 20 {
		t.Fatal("too few vectors")
	}
	return f.Vectors
}

func webhookAt(t *testing.T, secret string, now int64, opts ...WebhookOption) *Webhook {
	t.Helper()
	w, err := NewWebhook(secret, opts...)
	if err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return time.Unix(now, 0) }
	return w
}

func header(m map[string]string) http.Header {
	h := http.Header{}
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func TestWebhookVectors(t *testing.T) {
	for _, v := range loadVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			var opts []WebhookOption
			if v.Tolerance != nil {
				opts = append(opts, WithTolerance(time.Duration(*v.Tolerance)*time.Second))
			}
			w := webhookAt(t, v.Secret, v.Now, opts...)
			ev, err := w.Verify([]byte(v.Payload), header(v.Headers))
			if string(v.Expect) == "null" {
				var e *Error
				if !errors.As(err, &e) || e.Kind != KindWebhookVerification || !errors.Is(err, ErrWebhookVerification) || e.Message != v.Error {
					t.Fatalf("got %v, want %q", err, v.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(ev.Raw, v.Expect) {
				t.Fatalf("raw %s, want %s", ev.Raw, v.Expect)
			}
		})
	}
}

func TestWebhookSign(t *testing.T) {
	for _, v := range loadVectors(t) {
		switch v.Name {
		case "standard webhooks spec vector", "signed by the worker", "signed by the node sdk", "signed by the official library":
			w, _ := NewWebhook(v.Secret)
			var ts int64
			_ = json.Unmarshal([]byte(v.Headers["webhook-timestamp"]), &ts)
			if got := w.Sign(v.Headers["webhook-id"], time.Unix(ts, 0), []byte(v.Payload)); got != v.Headers["webhook-signature"] {
				t.Errorf("%s: %s != %s", v.Name, got, v.Headers["webhook-signature"])
			}
		}
	}
}

func TestWebhookEvent(t *testing.T) {
	var good vector
	for _, v := range loadVectors(t) {
		if v.Name == "signed by the node sdk" {
			good = v
		}
	}
	w := webhookAt(t, good.Secret, good.Now)

	ev, err := w.Verify([]byte(good.Payload), header(good.Headers))
	if err != nil || ev.Type != "invoice.paid" || ev.Timestamp == "" {
		t.Fatal(ev, err)
	}
	var data struct {
		InvoiceID string `json:"invoice_id"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil || data.InvoiceID != "inv_1" || data.Name != "請求書" {
		t.Fatal(data, err)
	}

	// headers built as a map literal with lowercase keys (not canonicalised)
	raw := http.Header{}
	for k, v := range good.Headers {
		raw[k] = []string{v}
	}
	if _, err := w.Verify([]byte(good.Payload), raw); err != nil {
		t.Fatal(err)
	}
	raw["webhook-id"] = []string{}
	if _, err := w.Verify([]byte(good.Payload), raw); !errors.Is(err, ErrWebhookVerification) {
		t.Fatal(err)
	}
}

func TestWebhookDefaults(t *testing.T) {
	w, err := NewWebhook("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw")
	if err != nil || w.tolerance != 5*time.Minute {
		t.Fatal(err)
	}
	now := time.Now()
	sig := w.Sign("msg_now", now, []byte(`{"type":"t","timestamp":"x","data":1}`))
	h := http.Header{}
	h.Set("webhook-id", "msg_now")
	h.Set("webhook-timestamp", strconv.FormatInt(now.Unix(), 10))
	h.Set("webhook-signature", sig)
	ev, err := w.Verify([]byte(`{"type":"t","timestamp":"x","data":1}`), h)
	if err != nil || string(ev.Data) != "1" {
		t.Fatal(ev, err)
	}
}

func TestWebhookSecret(t *testing.T) {
	if _, err := NewWebhook(""); !errors.Is(err, ErrInvalidArgument) || err.Error() != "Webhook secret is required" {
		t.Fatal(err)
	}
	_, err := NewWebhook("whsec_***")
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindInvalidArgument || e.Err == nil {
		t.Fatal(err)
	}
	if errors.Is(err, ErrWebhookVerification) {
		t.Fatal("secret errors are not verification errors")
	}
	// unpadded base64 is accepted, like atob in the Node SDK
	if _, err := NewWebhook("whsec_dGhpcyBpcyBhbm90aGVyIHNlY3JldCBrZXk"); err != nil {
		t.Fatal(err)
	}
}
