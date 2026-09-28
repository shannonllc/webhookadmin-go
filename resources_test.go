package webhookadmin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// Each method calls the documented method, path and body (docs/api.md, section 2).
func TestResources(t *testing.T) {
	ctx := context.Background()
	ok := func() reply { return jsonReply(200, map[string]any{"id": "x"}) }
	status := EndpointPaused
	cases := []struct {
		name   string
		reply  reply
		op     func(c *Client) error
		method string
		path   string
		body   string // "" means no body
	}{
		{"messages.send", ok(), func(c *Client) error {
			_, err := c.Messages.Send(ctx, SendMessageParams{Consumer: "cus_1", EventType: "invoice.paid", Payload: map[string]any{"a": []any{1, "二"}}}, WithIdempotencyKey("k1"))
			return err
		}, "POST", "/v1/messages", `{"consumer":"cus_1","event_type":"invoice.paid","payload":{"a":[1,"二"]}}`},
		{"messages.get escapes the id", ok(), func(c *Client) error { _, err := c.Messages.Get(ctx, "msg_1/../x"); return err }, "GET", "/v1/messages/msg_1%2F..%2Fx", ""},
		{"deliveries.retry", jsonReply(202, nil), func(c *Client) error { return c.Deliveries.Retry(ctx, "dlv_1") }, "POST", "/v1/deliveries/dlv_1/retry", ""},
		{"consumers.create", ok(), func(c *Client) error {
			_, err := c.Consumers.Create(ctx, ConsumerCreateParams{ExternalID: "cus_1"})
			return err
		}, "POST", "/v1/consumers", `{"external_id":"cus_1"}`},
		{"consumers.create with name", ok(), func(c *Client) error {
			_, err := c.Consumers.Create(ctx, ConsumerCreateParams{ExternalID: "cus_2", Name: "Acme"})
			return err
		}, "POST", "/v1/consumers", `{"external_id":"cus_2","name":"Acme"}`},
		{"endpoints.create minimal", ok(), func(c *Client) error {
			_, err := c.Endpoints.Create(ctx, EndpointCreateParams{ConsumerID: "con_1", URL: "https://x.example/wh"})
			return err
		}, "POST", "/v1/endpoints", `{"consumer_id":"con_1","url":"https://x.example/wh"}`},
		{"endpoints.create full", ok(), func(c *Client) error {
			_, err := c.Endpoints.Create(ctx, EndpointCreateParams{
				ConsumerID: "con_1", URL: "https://x.example/wh", EventTypes: []string{"invoice.paid"}, FixedIP: true, Description: "d",
				Retry:           &RetryPolicy{Count: 3, Interval: "5m"},
				CompatSignature: &CompatSignature{Header: "x-hub-signature-256", Content: "body", Encoding: "hex", Prefix: "sha256="},
			})
			return err
		}, "POST", "/v1/endpoints", `{"consumer_id":"con_1","url":"https://x.example/wh","event_types":["invoice.paid"],"fixed_ip":true,"description":"d","retry":{"count":3,"interval":"5m"},"compat_signature":{"header":"x-hub-signature-256","content":"body","encoding":"hex","prefix":"sha256="}}`},
		{"endpoints.get", ok(), func(c *Client) error { _, err := c.Endpoints.Get(ctx, "ep_1"); return err }, "GET", "/v1/endpoints/ep_1", ""},
		{"endpoints.update status", ok(), func(c *Client) error {
			_, err := c.Endpoints.Update(ctx, "ep_1", EndpointUpdateParams{Status: &status})
			return err
		}, "PATCH", "/v1/endpoints/ep_1", `{"status":"paused"}`},
		{"endpoints.update nulls", ok(), func(c *Client) error {
			_, err := c.Endpoints.Update(ctx, "ep_1", EndpointUpdateParams{
				URL: String("https://y"), Description: String(""),
				EventTypes: Null[[]string](), Retry: Null[RetryPolicy](), CompatSignature: Null[CompatSignature](),
			})
			return err
		}, "PATCH", "/v1/endpoints/ep_1", `{"compat_signature":null,"description":"","event_types":null,"retry":null,"url":"https://y"}`},
		{"endpoints.update values", ok(), func(c *Client) error {
			_, err := c.Endpoints.Update(ctx, "ep_1", EndpointUpdateParams{
				EventTypes: Value([]string{"a"}), Retry: Value(RetryPolicy{Count: 0, Interval: "1m"}),
				CompatSignature: Value(CompatSignature{Header: "x-sig", Content: "timestamp_body", Encoding: "base64"}),
			})
			return err
		}, "PATCH", "/v1/endpoints/ep_1", `{"compat_signature":{"header":"x-sig","content":"timestamp_body","encoding":"base64"},"event_types":["a"],"retry":{"count":0,"interval":"1m"}}`},
		{"endpoints.update nothing", ok(), func(c *Client) error {
			_, err := c.Endpoints.Update(ctx, "ep_1", EndpointUpdateParams{})
			return err
		}, "PATCH", "/v1/endpoints/ep_1", `{}`},
		{"endpoints.delete", jsonReply(204, nil), func(c *Client) error { return c.Endpoints.Delete(ctx, "ep_1") }, "DELETE", "/v1/endpoints/ep_1", ""},
		{"endpoints.rotate_secret", ok(), func(c *Client) error { _, err := c.Endpoints.RotateSecret(ctx, "ep_1"); return err }, "POST", "/v1/endpoints/ep_1/rotate-secret", ""},
		{"endpoints.recover", ok(), func(c *Client) error {
			_, err := c.Endpoints.Recover(ctx, "ep_1", RecoverParams{Since: 1790000000000})
			return err
		}, "POST", "/v1/endpoints/ep_1/recover", `{"since":1790000000000}`},
		{"endpoints.recovery", ok(), func(c *Client) error { _, err := c.Endpoints.Recovery(ctx, "ep_1", "rcv_1"); return err }, "GET", "/v1/endpoints/ep_1/recoveries/rcv_1", ""},
		{"endpoints.send_test", ok(), func(c *Client) error { _, err := c.Endpoints.SendTest(ctx, "ep_1", nil); return err }, "POST", "/v1/endpoints/ep_1/test", `{}`},
		{"endpoints.send_test event type", ok(), func(c *Client) error {
			_, err := c.Endpoints.SendTest(ctx, "ep_1", &EndpointSendTestParams{EventType: "user.created"})
			return err
		}, "POST", "/v1/endpoints/ep_1/test", `{"event_type":"user.created"}`},
		{"portal.create_link", ok(), func(c *Client) error { _, err := c.Portal.CreateLink(ctx, "con_1", nil); return err }, "POST", "/v1/consumers/con_1/portal", `{}`},
		{"portal.create_link params", ok(), func(c *Client) error {
			_, err := c.Portal.CreateLink(ctx, "con_1", &PortalLinkParams{FrameOrigin: "https://app.example.com", Locale: "en"})
			return err
		}, "POST", "/v1/consumers/con_1/portal", `{"frame_origin":"https://app.example.com","locale":"en"}`},
		{"event_types.list", jsonReply(200, map[string]any{"items": []any{}}), func(c *Client) error { _, err := c.EventTypes.List(ctx); return err }, "GET", "/v1/event-types", ""},
		{"event_types.create", ok(), func(c *Client) error {
			_, err := c.EventTypes.Create(ctx, EventTypeCreateParams{Name: "b", Description: "B"})
			return err
		}, "POST", "/v1/event-types", `{"name":"b","description":"B"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, tc.reply)
			c, _ := testClient(t, f)
			if err := tc.op(c); err != nil {
				t.Fatal(err)
			}
			got := f.Last()
			if got.Method != tc.method || got.Path != tc.path || string(got.Raw) != tc.body {
				t.Fatalf("got %s %s %s", got.Method, got.Path, got.Raw)
			}
			if (tc.body != "") != (got.Header.Get("Content-Type") == "application/json") {
				t.Fatal("content-type")
			}
		})
	}

	t.Run("errors from every method are returned", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(404, map[string]any{"error": "not_found", "message": "no"}))
		c, _ := testClient(t, f)
		ops := []func() error{
			func() error { _, err := c.Messages.Send(ctx, SendMessageParams{}); return err },
			func() error { _, err := c.Messages.Get(ctx, "m"); return err },
			func() error { _, err := c.Consumers.Create(ctx, ConsumerCreateParams{}); return err },
			func() error { _, err := c.Endpoints.Create(ctx, EndpointCreateParams{}); return err },
			func() error { _, err := c.Endpoints.List(ctx, nil); return err },
			func() error { _, err := c.Endpoints.Get(ctx, "e"); return err },
			func() error { _, err := c.Endpoints.Update(ctx, "e", EndpointUpdateParams{}); return err },
			func() error { return c.Endpoints.Delete(ctx, "e") },
			func() error { _, err := c.Endpoints.RotateSecret(ctx, "e"); return err },
			func() error { _, err := c.Endpoints.Recover(ctx, "e", RecoverParams{}); return err },
			func() error { _, err := c.Endpoints.Recovery(ctx, "e", "r"); return err },
			func() error { _, err := c.Endpoints.SendTest(ctx, "e", nil); return err },
			func() error { _, err := c.Portal.CreateLink(ctx, "c", nil); return err },
			func() error { _, err := c.EventTypes.List(ctx); return err },
			func() error { _, err := c.EventTypes.Create(ctx, EventTypeCreateParams{}); return err },
			func() error { _, err := c.EventTypes.Ensure(ctx, nil); return err },
		}
		for i, op := range ops {
			if err := op(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("op %d: %v", i, err)
			}
		}
	})

	t.Run("decoding", func(t *testing.T) {
		detail := `{"id":"ep_1","consumer_id":"con_1","url":"https://x","description":"","event_types":null,"fixed_ip":false,"status":"active","disabled_reason":null,"failing_since":null,"created_at":1,"retry":{"count":7,"interval":"progressive"},"compat_signature":null,"recent":[{"id":"att_1","at":2,"response_status":200,"response_head":null,"duration_ms":12,"error":null,"via":"direct","source_ip":null}],"retrying_count":0,"recovery":null}`
		f := newFakeAPI(t, rawReply(200, detail))
		c, _ := testClient(t, f)
		d, err := c.Endpoints.Get(ctx, "ep_1")
		if err != nil || d.ID != "ep_1" || d.EventTypes != nil || d.Retry.Count != 7 || len(d.Recent) != 1 || *d.Recent[0].ResponseStatus != 200 {
			t.Fatal(d, err)
		}
		msg := `{"id":"msg_1","event_type":"e","consumer":{"id":"con_1","external_id":"cus_1","name":""},"created_at":1,"status":"success","idempotency_key":"k","payload":{"n":1},"deliveries":[]}`
		f2 := newFakeAPI(t, rawReply(200, msg))
		c2, _ := testClient(t, f2)
		m, err := c2.Messages.Get(ctx, "msg_1")
		if err != nil || string(m.Payload) != `{"n":1}` || *m.IdempotencyKey != "k" || m.Consumer.ExternalID != "cus_1" {
			t.Fatal(m, err)
		}
	})

	t.Run("endpoints list", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(200, map[string]any{"items": []any{map[string]any{"id": "ep_1"}}}))
		c, _ := testClient(t, f)
		p, err := c.Endpoints.List(ctx, &EndpointListParams{ConsumerID: "con_1"})
		if err != nil || len(p.Items) != 1 || p.NextCursor != "" || f.Last().Query != "consumer_id=con_1" {
			t.Fatal(p, err)
		}
	})
}

func et(name, description string) map[string]any {
	return map[string]any{"name": name, "description": description, "archived_at": nil}
}

func TestEnsureEventTypes(t *testing.T) {
	ctx := context.Background()
	t.Run("creates missing ones, keeps existing ones", func(t *testing.T) {
		f := newFakeAPI(t,
			jsonReply(200, map[string]any{"items": []any{et("a", "kept")}}),
			jsonReply(201, et("b", "B")),
			jsonReply(201, et("c", "")),
		)
		c, _ := testClient(t, f)
		r, err := c.EventTypes.Ensure(ctx, []EventTypeCreateParams{{Name: "a", Description: "new"}, {Name: "b", Description: "B"}, {Name: "c"}, {Name: "b"}})
		if err != nil {
			t.Fatal(err)
		}
		names := func(ts []EventType) (out []string) {
			for _, t := range ts {
				out = append(out, t.Name+":"+t.Description)
			}
			return out
		}
		if !reflect.DeepEqual(names(r.Created), []string{"b:B", "c:"}) || !reflect.DeepEqual(names(r.Existing), []string{"a:kept"}) {
			t.Fatal(r)
		}
		var bodies []string
		for _, cl := range f.Calls() {
			bodies = append(bodies, string(cl.Raw))
		}
		if !reflect.DeepEqual(bodies, []string{"", `{"name":"b","description":"B"}`, `{"name":"c"}`}) {
			t.Fatal(bodies)
		}
	})

	t.Run("a name registered meanwhile is existing", func(t *testing.T) {
		f := newFakeAPI(t,
			jsonReply(200, map[string]any{"items": []any{}}),
			jsonReply(409, map[string]any{"error": "conflict", "message": "exists"}),
			jsonReply(200, map[string]any{"items": []any{et("a", "theirs"), et("z", "")}}),
		)
		c, _ := testClient(t, f)
		r, err := c.EventTypes.Ensure(ctx, []EventTypeCreateParams{{Name: "a"}})
		if err != nil || len(r.Created) != 0 || len(r.Existing) != 1 || r.Existing[0].Description != "theirs" {
			t.Fatal(r, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(200, map[string]any{"items": []any{}}), jsonReply(403, map[string]any{"error": "forbidden", "message": "x"}))
		c, _ := testClient(t, f)
		if _, err := c.EventTypes.Ensure(ctx, []EventTypeCreateParams{{Name: "a"}}); !errors.Is(err, ErrPermission) {
			t.Fatal(err)
		}
		f2 := newFakeAPI(t, jsonReply(200, map[string]any{"items": []any{}}), jsonReply(409, map[string]any{"error": "conflict", "message": "x"}), jsonReply(500, map[string]any{}))
		c2, _ := testClient(t, f2, WithMaxRetries(0))
		if _, err := c2.EventTypes.Ensure(ctx, []EventTypeCreateParams{{Name: "a"}}); !errors.Is(err, ErrAPI) {
			t.Fatal(err)
		}
	})
}

func TestNullable(t *testing.T) {
	var unset Nullable[int]
	if unset.IsSet() || unset.IsNull() {
		t.Fatal("zero value")
	}
	if n := Null[int](); !n.IsSet() || !n.IsNull() {
		t.Fatal("null")
	}
	if v := Value(3); !v.IsSet() || v.IsNull() || v.jsonValue() != 3 {
		t.Fatal("value")
	}
	b, _ := json.Marshal(EndpointUpdateParams{EventTypes: Value([]string(nil))})
	if string(b) != `{"event_types":null}` {
		t.Fatal(string(b))
	}
}
