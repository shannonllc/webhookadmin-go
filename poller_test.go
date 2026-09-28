package webhookadmin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func polledMsg(id string) map[string]any {
	return map[string]any{
		"id":         id,
		"event_type": "invoice.paid",
		"timestamp":  "2026-09-29T00:00:00.000Z",
		"payload":    map[string]any{"type": "invoice.paid", "timestamp": "2026-09-29T00:00:00.000Z", "data": map[string]any{"id": id}},
		"headers":    map[string]any{"webhook-id": id, "webhook-timestamp": "1790000000", "webhook-signature": "v1,x"},
	}
}

func pollReply(iterator string, done bool, ids ...string) reply {
	data := []any{}
	for _, id := range ids {
		data = append(data, polledMsg(id))
	}
	return jsonReply(200, map[string]any{"data": data, "iterator": iterator, "done": done})
}

func testPoller(t *testing.T, f *fakeAPI, opts ...Option) *Poller {
	t.Helper()
	p, err := NewPoller("ep_1", "sk_poll_abc", append([]Option{WithBaseURL(f.srv.URL + "/")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	s := &sleeps{}
	p.c.sleep = s.sleep
	p.c.random = func() float64 { return 0.5 }
	return p
}

func TestPoller(t *testing.T) {
	ctx := context.Background()

	t.Run("poll sends the token, the iterator and the limit", func(t *testing.T) {
		f := newFakeAPI(t, pollReply("it_1", false, "msg_1"))
		p := testPoller(t, f)
		if p.EndpointID() != "ep_1" || p.BaseURL() != f.srv.URL {
			t.Fatal(p.EndpointID(), p.BaseURL())
		}
		page, err := p.Poll(ctx, &PollParams{Iterator: "it_0", Limit: 10})
		if err != nil || page.Iterator != "it_1" || page.Done || len(page.Data) != 1 {
			t.Fatal(page, err)
		}
		m := page.Data[0]
		if m.ID != "msg_1" || m.EventType != "invoice.paid" || m.Headers["webhook-id"] != "msg_1" || string(m.Payload) == "" {
			t.Fatal(m)
		}
		got := f.Last()
		if got.Method != "GET" || got.Path != "/v1/poller/ep_1" || got.Query != "iterator=it_0&limit=10" {
			t.Fatal(got)
		}
		if got.Header.Get("Authorization") != "Bearer sk_poll_abc" || got.Header.Get("User-Agent") != UserAgent {
			t.Fatal(got.Header)
		}
		if _, err := p.Poll(ctx, nil); err != nil || f.Last().Query != "" {
			t.Fatal(f.Last().Query, err)
		}
	})

	t.Run("pages follow the iterator and stop after done", func(t *testing.T) {
		f := newFakeAPI(t, pollReply("it_1", false, "msg_1", "msg_2"), pollReply("it_2", true, "msg_3"))
		it := testPoller(t, f).Pages(&PollParams{Iterator: "it_0", Limit: 2})
		var seen []string
		for it.Next(ctx) {
			seen = append(seen, it.Current().Iterator)
		}
		if it.Err() != nil || !reflect.DeepEqual(seen, []string{"it_1", "it_2"}) {
			t.Fatal(seen, it.Err())
		}
		if it.Next(ctx) {
			t.Fatal("next after done")
		}
		calls := f.Calls()
		if len(calls) != 2 || calls[0].Query != "iterator=it_0&limit=2" || calls[1].Query != "iterator=it_1&limit=2" {
			t.Fatal(calls)
		}
	})

	t.Run("event types and after", func(t *testing.T) {
		f := newFakeAPI(t, pollReply("i", true), pollReply("i", true))
		p := testPoller(t, f)
		after := time.Date(2026, 9, 29, 9, 0, 0, 0, time.FixedZone("JST", 9*3600))
		if _, err := p.Poll(ctx, &PollParams{EventTypes: []string{"invoice.paid", "invoice.failed"}, After: after}); err != nil {
			t.Fatal(err)
		}
		if got := f.Last().Query; got != "after=2026-09-29T00%3A00%3A00Z&event_type=invoice.paid&event_type=invoice.failed" {
			t.Fatal(got)
		}
		it := p.Messages(&PollParams{EventTypes: []string{"a.b"}})
		for it.Next(ctx) {
		}
		if got := f.Last().Query; it.Err() != nil || got != "event_type=a.b" {
			t.Fatal(got, it.Err())
		}
	})

	t.Run("pages keep the filters", func(t *testing.T) {
		f := newFakeAPI(t, pollReply("it_1", false, "msg_1"), pollReply("it_1", true))
		it := testPoller(t, f).Pages(&PollParams{EventTypes: []string{"a.b"}})
		for it.Next(ctx) {
		}
		if calls := f.Calls(); it.Err() != nil || len(calls) != 2 || calls[0].Query != "event_type=a.b" || calls[1].Query != "event_type=a.b&iterator=it_1" {
			t.Fatal(calls, it.Err())
		}
	})

	t.Run("messages flatten the pages", func(t *testing.T) {
		f := newFakeAPI(t, pollReply("it_1", false, "msg_1"), pollReply("it_1", false), pollReply("it_2", true, "msg_2"))
		it := testPoller(t, f).Messages(nil)
		var ids []string
		for it.Next(ctx) {
			ids = append(ids, it.Current().ID)
		}
		if it.Err() != nil || !reflect.DeepEqual(ids, []string{"msg_1", "msg_2"}) {
			t.Fatal(ids, it.Err())
		}
		if calls := f.Calls(); len(calls) != 3 || calls[0].Query != "" || calls[2].Query != "iterator=it_1" {
			t.Fatal(calls)
		}
	})

	t.Run("5xx is retried; errors stop the iterators", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(503, map[string]any{"error": "unavailable", "message": "x"}), pollReply("i", true))
		if _, err := testPoller(t, f, WithMaxRetries(1)).Poll(ctx, nil); err != nil || len(f.Calls()) != 2 {
			t.Fatal(err)
		}
		bad := newFakeAPI(t, jsonReply(401, map[string]any{"error": "unauthorized", "message": "bad token"}))
		p := testPoller(t, bad)
		pages := p.Pages(nil)
		if pages.Next(ctx) || !errors.Is(pages.Err(), ErrAuthentication) || pages.Next(ctx) {
			t.Fatal(pages.Err())
		}
		msgs := p.Messages(&PollParams{Limit: 5})
		if msgs.Next(ctx) || !errors.Is(msgs.Err(), ErrAuthentication) {
			t.Fatal(msgs.Err())
		}
	})

	t.Run("requires the endpoint ID and the token", func(t *testing.T) {
		t.Setenv(APIKeyEnv, "sk_test_env")
		if _, err := NewPoller("", "sk_poll_abc"); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		if _, err := NewPoller("ep_1", ""); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		p, err := NewPoller("ep_1", "sk_poll_abc")
		if err != nil || p.BaseURL() != DefaultBaseURL {
			t.Fatal(p, err)
		}
	})
}
