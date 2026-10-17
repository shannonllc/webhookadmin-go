package webhookadmin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var emptyPage = map[string]any{"items": []any{}, "next_cursor": nil}

func TestClientOptions(t *testing.T) {
	t.Run("headers", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(200, emptyPage))
		c, _ := testClient(t, f)
		if _, err := c.Consumers.List(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		h := f.Last().Header
		for k, want := range map[string]string{"Authorization": "Bearer sk_test_abc", "Accept": "application/json", "User-Agent": "webhookadmin-go/" + Version} {
			if got := h.Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		if h.Get("Content-Type") != "" || len(f.Last().Raw) != 0 {
			t.Errorf("GET sent a body")
		}
	})

	t.Run("user agent is parsed by the server", func(t *testing.T) {
		// サーバーが User-Agent を読むときと同じ形の正規表現
		if !regexp.MustCompile(`(?:^|\s)(webhookadmin-[a-z]+)/(\d+\.\d+\.\d+)(?:\s|$)`).MatchString(UserAgent) {
			t.Fatal(UserAgent)
		}
	})

	t.Run("api key from env", func(t *testing.T) {
		t.Setenv(APIKeyEnv, " sk_live_env ")
		f := newFakeAPI(t, jsonReply(200, emptyPage))
		c, err := NewClient("", WithBaseURL(f.srv.URL))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Consumers.List(context.Background(), nil)
		if got := f.Last().Header.Get("Authorization"); got != "Bearer sk_live_env" {
			t.Fatal(got)
		}
	})

	t.Run("missing api key", func(t *testing.T) {
		t.Setenv(APIKeyEnv, "  ")
		_, err := NewClient("")
		if !errors.Is(err, ErrInvalidArgument) || !strings.Contains(err.Error(), APIKeyEnv) {
			t.Fatal(err)
		}
	})

	t.Run("base url", func(t *testing.T) {
		c, _ := NewClient("sk_test_x")
		if c.BaseURL() != "https://api.webhookadmin.com" {
			t.Fatal(c.BaseURL())
		}
		c, _ = NewClient("sk_test_x", WithBaseURL("http://api.example.test//"))
		if c.BaseURL() != "http://api.example.test" {
			t.Fatal(c.BaseURL())
		}
		if c.timeout != DefaultTimeout || c.maxRetries != DefaultMaxRetries || c.httpClient == nil {
			t.Fatal("defaults")
		}
		h := &http.Client{}
		c, _ = NewClient("sk_test_x", WithHTTPClient(h), WithTimeout(time.Second), WithMaxRetries(5))
		if c.httpClient != h || c.timeout != time.Second || c.maxRetries != 5 {
			t.Fatal("options")
		}
	})

	t.Run("version matches the changelog", func(t *testing.T) {
		b, err := os.ReadFile("CHANGELOG.md")
		if err != nil || !strings.Contains(string(b), "## "+Version) {
			t.Fatal("CHANGELOG.md has no entry for", Version)
		}
	})
}

func TestErrors(t *testing.T) {
	cases := []struct {
		status   int
		code     string
		kind     Kind
		sentinel error
	}{
		{400, "bad_json", KindValidation, ErrValidation},
		{401, "unauthorized", KindAuthentication, ErrAuthentication},
		{402, "plan_limit", KindPlanLimit, ErrPlanLimit},
		{403, "forbidden", KindPermission, ErrPermission},
		{404, "not_found", KindNotFound, ErrNotFound},
		{409, "conflict", KindConflict, ErrConflict},
		{413, "too_large", KindValidation, ErrValidation},
		{422, "invalid", KindValidation, ErrValidation},
		{418, "teapot", KindAPI, ErrAPI},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			f := newFakeAPI(t, jsonReply(tc.status, map[string]any{"error": tc.code, "message": "m"}, "cf-ray", "ray-1"))
			c, _ := testClient(t, f)
			_, err := c.Messages.Send(context.Background(), SendMessageParams{Consumer: "c", EventType: "e", Payload: 1})
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%T", err)
			}
			if e.Kind != tc.kind || e.Status != tc.status || e.Code != tc.code || e.Message != "m" || e.RequestID != "ray-1" || e.Fields != nil {
				t.Fatalf("%+v", e)
			}
			if !errors.Is(err, tc.sentinel) || errors.Is(err, ErrConnection) || err.Error() != "m" {
				t.Fatal("sentinel")
			}
			if len(f.Calls()) != 1 {
				t.Fatal("retried")
			}
		})
	}

	t.Run("fields and request id", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(422, map[string]any{"error": "invalid", "message": "bad", "fields": map[string]any{"url": "https only", "n": 1}}, "x-request-id", "req_1", "cf-ray", "r"))
		c, _ := testClient(t, f)
		_, err := c.Endpoints.Create(context.Background(), EndpointCreateParams{ConsumerID: "c", URL: "http://x"})
		var e *Error
		errors.As(err, &e)
		if !reflect.DeepEqual(e.Fields, map[string]string{"url": "https only", "n": "1"}) || e.RequestID != "req_1" {
			t.Fatalf("%+v", e)
		}
	})

	t.Run("non-json error body", func(t *testing.T) {
		f := newFakeAPI(t, rawReply(502, "<html>bad gateway</html>"), rawReply(400, "null"), rawReply(500, `"oops"`))
		c, _ := testClient(t, f, WithMaxRetries(0))
		for _, want := range []struct {
			status int
			kind   Kind
		}{{502, KindAPI}, {400, KindValidation}, {500, KindAPI}} {
			_, err := c.Messages.Get(context.Background(), "m")
			var e *Error
			errors.As(err, &e)
			if e.Status != want.status || e.Kind != want.kind || e.Code != "" || e.Message != "HTTP "+strconv.Itoa(want.status) || e.RequestID != "" {
				t.Fatalf("%+v", e)
			}
		}
	})

	t.Run("invalid json in 2xx is not retried", func(t *testing.T) {
		f := newFakeAPI(t, rawReply(200, "{oops", "cf-ray", "r2"))
		c, _ := testClient(t, f)
		_, err := c.Messages.Get(context.Background(), "m")
		var e *Error
		errors.As(err, &e)
		if e.Kind != KindAPI || e.Status != 200 || e.RequestID != "r2" || !strings.Contains(e.Message, "Invalid JSON") || len(f.Calls()) != 1 {
			t.Fatalf("%+v", e)
		}
	})

	t.Run("json of the wrong shape in 2xx", func(t *testing.T) {
		f := newFakeAPI(t, rawReply(200, `{"id": 1}`))
		c, _ := testClient(t, f)
		_, err := c.Messages.Get(context.Background(), "m")
		var e *Error
		errors.As(err, &e)
		if e.Kind != KindAPI || !strings.Contains(e.Message, "Unexpected response") || e.Unwrap() == nil {
			t.Fatalf("%+v", e)
		}
	})

	t.Run("connection error keeps the cause", func(t *testing.T) {
		f := newFakeAPI(t, dropReply)
		c, _ := testClient(t, f, WithMaxRetries(0))
		_, err := c.Messages.Get(context.Background(), "m")
		var e *Error
		errors.As(err, &e)
		if e.Kind != KindConnection || e.Status != 0 || e.Err == nil || !strings.HasPrefix(e.Message, "Connection error: ") {
			t.Fatalf("%+v", e)
		}
		if !errors.Is(err, ErrConnection) || errors.Is(err, ErrTimeout) {
			t.Fatal("sentinel")
		}
	})

	t.Run("unencodable body", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(202, map[string]any{}))
		c, _ := testClient(t, f)
		_, err := c.Messages.Send(context.Background(), SendMessageParams{Payload: make(chan int)})
		if !errors.Is(err, ErrInvalidArgument) || len(f.Calls()) != 0 {
			t.Fatal(err)
		}
	})

	t.Run("invalid base url", func(t *testing.T) {
		c, _ := NewClient("sk_test_x", WithBaseURL("http://bad host"))
		_, err := c.Messages.Get(context.Background(), "m")
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
	})
}

func TestRetries(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		replies   []reply
		opts      []Option
		op        func(c *Client) error
		calls     int
		sleeps    []time.Duration
		wantError error
	}{
		{
			name:    "5xx with backoff, then success",
			replies: []reply{jsonReply(500, map[string]any{"error": "internal", "message": "x"}), jsonReply(503, map[string]any{}), jsonReply(200, map[string]any{"id": "msg_1"})},
			op:      func(c *Client) error { _, err := c.Messages.Get(ctx, "msg_1"); return err },
			calls:   3, sleeps: []time.Duration{500 * time.Millisecond, time.Second},
		},
		{
			name:    "gives up after max retries",
			replies: []reply{jsonReply(500, map[string]any{"error": "internal", "message": "down"})},
			opts:    []Option{WithMaxRetries(3)},
			op:      func(c *Client) error { _, err := c.Messages.Get(ctx, "m"); return err },
			calls:   4, sleeps: []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}, wantError: ErrAPI,
		},
		{
			name:    "max retries per request",
			replies: []reply{jsonReply(500, map[string]any{})},
			opts:    []Option{WithMaxRetries(5)},
			op:      func(c *Client) error { _, err := c.Messages.Get(ctx, "m", WithRequestMaxRetries(0)); return err },
			calls:   1, wantError: ErrAPI,
		},
		{
			name:    "connection errors retried for safe requests",
			replies: []reply{dropReply, jsonReply(200, emptyPage)},
			op:      func(c *Client) error { _, err := c.Consumers.List(ctx, nil); return err },
			calls:   2, sleeps: []time.Duration{500 * time.Millisecond},
		},
		{
			name:    "4xx not retried",
			replies: []reply{jsonReply(409, map[string]any{"error": "conflict", "message": "x"})},
			op:      func(c *Client) error { _, err := c.Messages.Get(ctx, "m"); return err },
			calls:   1, wantError: ErrConflict,
		},
		{
			name:    "429 waits retry-after, for any request",
			replies: []reply{jsonReply(429, map[string]any{"error": "rate_limited", "message": "slow"}, "retry-after", "7"), jsonReply(201, map[string]any{"id": "con_1"})},
			op: func(c *Client) error {
				_, err := c.Consumers.Create(ctx, ConsumerCreateParams{ExternalID: "x"})
				return err
			},
			calls: 2, sleeps: []time.Duration{7 * time.Second},
		},
		{
			name:    "429 without headers uses backoff",
			replies: []reply{jsonReply(429, map[string]any{"error": "rate_limited", "message": "x"}), jsonReply(200, map[string]any{"id": "m"})},
			op:      func(c *Client) error { _, err := c.Messages.Get(ctx, "m"); return err },
			calls:   2, sleeps: []time.Duration{500 * time.Millisecond},
		},
		{
			name:    "429 longer than a minute returned at once",
			replies: []reply{jsonReply(429, map[string]any{"error": "rate_limited", "message": "x"}, "retry-after", "3600")},
			op:      func(c *Client) error { _, err := c.Messages.Get(ctx, "m"); return err },
			calls:   1, wantError: ErrRateLimit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, tc.replies...)
			c, s := testClient(t, f, tc.opts...)
			err := tc.op(c)
			if tc.wantError == nil && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != nil && !errors.Is(err, tc.wantError) {
				t.Fatalf("got %v, want %v", err, tc.wantError)
			}
			if n := len(f.Calls()); n != tc.calls {
				t.Fatalf("calls = %d, want %d", n, tc.calls)
			}
			if got := s.All(); !reflect.DeepEqual(got, tc.sleeps) && !(len(got) == 0 && len(tc.sleeps) == 0) {
				t.Fatalf("sleeps = %v, want %v", got, tc.sleeps)
			}
		})
	}

	t.Run("429 waits until x-ratelimit-reset", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(429, map[string]any{"error": "rate_limited", "message": "x"}, "x-ratelimit-reset", "1012"), jsonReply(200, map[string]any{"id": "m"}))
		c, s := testClient(t, f)
		c.now = func() time.Time { return time.Unix(1000, 0) }
		if _, err := c.Messages.Get(ctx, "m"); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s.All(), []time.Duration{12 * time.Second}) {
			t.Fatal(s.All())
		}
	})

	t.Run("429 returned with RetryAfter", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(429, map[string]any{"error": "rate_limited", "message": "x"}, "retry-after", "1"))
		c, _ := testClient(t, f, WithMaxRetries(1))
		_, err := c.Messages.Get(ctx, "m")
		var e *Error
		errors.As(err, &e)
		if e.Kind != KindRateLimit || e.RetryAfter == nil || *e.RetryAfter != time.Second || len(f.Calls()) != 2 {
			t.Fatalf("%+v", e)
		}
	})

	t.Run("send reuses the idempotency key", func(t *testing.T) {
		f := newFakeAPI(t, dropReply, jsonReply(502, map[string]any{}), jsonReply(202, map[string]any{"id": "msg_1", "deliveries": 1}))
		c, _ := testClient(t, f)
		r, err := c.Messages.Send(ctx, SendMessageParams{Consumer: "c", EventType: "e", Payload: 1})
		if err != nil || r.ID != "msg_1" || r.Deliveries != 1 {
			t.Fatal(r, err)
		}
		keys := map[string]bool{}
		for _, cl := range f.Calls() {
			keys[cl.Header.Get("Idempotency-Key")] = true
		}
		if len(f.Calls()) != 3 || len(keys) != 1 {
			t.Fatal(keys)
		}
		for k := range keys {
			if !regexp.MustCompile(`^webhookadmin-go-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(k) {
				t.Fatal(k)
			}
		}
		_, _ = c.Messages.Send(ctx, SendMessageParams{}, WithIdempotencyKey("invoice-1-paid"))
		if f.Last().Header.Get("Idempotency-Key") != "invoice-1-paid" {
			t.Fatal("given key")
		}
	})

	t.Run("unsafe requests are not retried on 5xx, connection errors or timeouts", func(t *testing.T) {
		for _, rep := range []reply{jsonReply(500, map[string]any{}), dropReply, hangReply} {
			f := newFakeAPI(t, rep)
			c, _ := testClient(t, f, WithTimeout(50*time.Millisecond))
			ops := []func() error{
				func() error { _, err := c.Consumers.Create(ctx, ConsumerCreateParams{ExternalID: "x"}); return err },
				func() error {
					_, err := c.Endpoints.Create(ctx, EndpointCreateParams{ConsumerID: "c", URL: "https://x"})
					return err
				},
				func() error { _, err := c.Endpoints.RotateSecret(ctx, "ep"); return err },
				func() error { _, err := c.Endpoints.SendTest(ctx, "ep", nil); return err },
				func() error { _, err := c.Endpoints.Recover(ctx, "ep", RecoverParams{Since: 1}); return err },
				func() error { return c.Deliveries.Retry(ctx, "d") },
				func() error { _, err := c.EventTypes.Create(ctx, EventTypeCreateParams{Name: "x"}); return err },
			}
			for _, op := range ops {
				var e *Error
				if !errors.As(op(), &e) {
					t.Fatal("no error")
				}
			}
			if len(f.Calls()) != len(ops) {
				t.Fatalf("calls = %d", len(f.Calls()))
			}
		}
	})
}

func TestTimeoutAndCancel(t *testing.T) {
	t.Run("each attempt times out; safe requests are retried", func(t *testing.T) {
		f := newFakeAPI(t, hangReply)
		c, _ := testClient(t, f, WithTimeout(50*time.Millisecond), WithMaxRetries(1))
		_, err := c.Messages.Get(context.Background(), "m")
		var e *Error
		errors.As(err, &e)
		if e.Kind != KindTimeout || !errors.Is(err, ErrTimeout) || !errors.Is(err, ErrConnection) || e.Message != "Request timed out after 50ms" {
			t.Fatalf("%+v", e)
		}
		if len(f.Calls()) != 2 {
			t.Fatal(len(f.Calls()))
		}
	})

	t.Run("timeout per request", func(t *testing.T) {
		f := newFakeAPI(t, hangReply)
		c, _ := testClient(t, f, WithTimeout(time.Minute))
		_, err := c.Endpoints.RotateSecret(context.Background(), "ep", WithRequestTimeout(20*time.Millisecond))
		if !errors.Is(err, ErrTimeout) || len(f.Calls()) != 1 {
			t.Fatal(err)
		}
	})

	t.Run("http.Client timeout counts as a timeout", func(t *testing.T) {
		f := newFakeAPI(t, hangReply)
		c, _ := testClient(t, f, WithHTTPClient(&http.Client{Timeout: 20 * time.Millisecond}), WithMaxRetries(0))
		_, err := c.Messages.Get(context.Background(), "m")
		if !errors.Is(err, ErrTimeout) {
			t.Fatal(err)
		}
	})

	t.Run("a cancelled context stops the request and is not retried", func(t *testing.T) {
		f := newFakeAPI(t, hangReply)
		c, _ := testClient(t, f)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(30*time.Millisecond, cancel)
		_, err := c.Messages.Get(ctx, "m")
		var e *Error
		errors.As(err, &e)
		if e.Kind != KindConnection || e.Message != "Request was aborted" || len(f.Calls()) != 1 {
			t.Fatalf("%+v", e)
		}
	})

	t.Run("a context cancelled before the call sends nothing", func(t *testing.T) {
		f := newFakeAPI(t, hangReply)
		c, _ := testClient(t, f)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.Messages.Get(ctx, "m")
		if !errors.Is(err, ErrConnection) || !errors.Is(err, context.Canceled) || len(f.Calls()) != 0 {
			t.Fatal(err)
		}
	})

	t.Run("cancelling during the wait between retries", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(503, map[string]any{}))
		c, _ := testClient(t, f)
		ctx, cancel := context.WithCancel(context.Background())
		c.sleep = func(context.Context, time.Duration) error { cancel(); return context.Canceled }
		_, err := c.Messages.Get(ctx, "m")
		if !errors.Is(err, ErrConnection) || !errors.Is(err, context.Canceled) || len(f.Calls()) != 1 {
			t.Fatal(err)
		}
	})

	t.Run("real sleep honours the context", func(t *testing.T) {
		if err := sleepContext(context.Background(), time.Millisecond); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestPagination(t *testing.T) {
	ctx := context.Background()
	t.Run("list gives one page; ListAll walks every page", func(t *testing.T) {
		f := newFakeAPI(t,
			jsonReply(200, map[string]any{"items": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}, "next_cursor": "c1"}),
			jsonReply(200, map[string]any{"items": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}, "next_cursor": "c1"}),
			jsonReply(200, map[string]any{"items": []any{map[string]any{"id": "c"}}, "next_cursor": "c2"}),
			jsonReply(200, map[string]any{"items": []any{}, "next_cursor": nil}),
		)
		c, _ := testClient(t, f)
		p, err := c.Messages.List(ctx, &MessageListParams{Limit: 2, Status: MessageFailed, Since: 1790000000000})
		if err != nil || len(p.Items) != 2 || p.NextCursor != "c1" {
			t.Fatal(p, err)
		}
		var ids []string
		it := c.Messages.ListAll(&MessageListParams{Limit: 2, Status: MessageFailed})
		for it.Next(ctx) {
			ids = append(ids, it.Current().ID)
		}
		if it.Err() != nil || !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
			t.Fatal(ids, it.Err())
		}
		var qs []string
		for _, cl := range f.Calls() {
			qs = append(qs, cl.Query)
		}
		want := []string{"limit=2&since=1790000000000&status=failed", "limit=2&status=failed", "cursor=c1&limit=2&status=failed", "cursor=c2&limit=2&status=failed"}
		if !reflect.DeepEqual(qs, want) {
			t.Fatal(qs)
		}
		if it.Next(ctx) {
			t.Fatal("Next after the end")
		}
	})

	t.Run("consumers: starts at the given cursor; an error stops the iterator", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(200, map[string]any{"items": []any{map[string]any{"id": "x"}}, "next_cursor": "n"}), jsonReply(404, map[string]any{"error": "not_found", "message": "gone"}))
		c, _ := testClient(t, f, WithMaxRetries(0))
		it := c.Consumers.ListAll(&ConsumerListParams{Cursor: "start"})
		var got []string
		for it.Next(ctx) {
			got = append(got, it.Current().ID)
		}
		if !errors.Is(it.Err(), ErrNotFound) || !reflect.DeepEqual(got, []string{"x"}) || f.Calls()[0].Query != "cursor=start" {
			t.Fatal(got, it.Err())
		}
		if it.Next(ctx) {
			t.Fatal("Next after an error")
		}
	})

	t.Run("nil params and list errors", func(t *testing.T) {
		f := newFakeAPI(t, jsonReply(401, map[string]any{"error": "unauthorized", "message": "no"}))
		c, _ := testClient(t, f)
		if _, err := c.Messages.List(ctx, nil); !errors.Is(err, ErrAuthentication) {
			t.Fatal(err)
		}
		if _, err := c.Consumers.List(ctx, &ConsumerListParams{Limit: 5}); !errors.Is(err, ErrAuthentication) {
			t.Fatal(err)
		}
		if f.Calls()[0].Query != "" || f.Last().Query != "limit=5" {
			t.Fatal(f.Calls())
		}
		it := c.Messages.ListAll(nil)
		if it.Next(ctx) || !errors.Is(it.Err(), ErrAuthentication) {
			t.Fatal(it.Err())
		}
	})
}

func TestHelpers(t *testing.T) {
	half := func() float64 { return 0.5 }
	var got []time.Duration
	for _, n := range []int{0, 1, 2, 3, 4, 5, 10} {
		got = append(got, backoff(n, half))
	}
	ms := time.Millisecond
	if !reflect.DeepEqual(got, []time.Duration{500 * ms, 1000 * ms, 2000 * ms, 4000 * ms, 8000 * ms, 8000 * ms, 8000 * ms}) {
		t.Fatal(got)
	}
	if backoff(0, func() float64 { return 0 }) != 375*ms || backoff(0, func() float64 { return 1 }) != 625*ms {
		t.Fatal("jitter")
	}

	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	at := func(sec float64) time.Time { return time.Unix(0, int64(sec*1e9)) }
	cases := []struct {
		h    http.Header
		now  float64
		want time.Duration
		ok   bool
	}{
		{h("retry-after", "2"), 0, 2 * time.Second, true},
		{h("retry-after", "0.4"), 0, 400 * ms, true},
		{h("retry-after", "Thu, 01 Jan 1970 00:00:05 GMT"), 1, 4 * time.Second, true},
		{h("retry-after", "Thu, 01 Jan 1970 00:00:05 GMT"), 9, 0, true},
		{h("retry-after", "soon", "x-ratelimit-reset", "10"), 4, 6 * time.Second, true},
		{h("retry-after", "-3"), 0, 0, true},
		{h("retry-after", "NaN"), 0, 0, false},
		{h("x-ratelimit-reset", "1"), 5, 0, true},
		{h("x-ratelimit-reset", "x"), 0, 0, false},
		{h("x-ratelimit-reset", "Inf"), 0, 0, false},
		{h("retry-after", " "), 0, 0, false},
		{h(), 0, 0, false},
	}
	for i, tc := range cases {
		d, ok := rateLimitWait(tc.h, at(tc.now))
		if d != tc.want || ok != tc.ok {
			t.Errorf("case %d: got %v %v, want %v %v", i, d, ok, tc.want, tc.ok)
		}
	}
	if MaxRateLimitWait != time.Minute {
		t.Fatal(MaxRateLimitWait)
	}
	if *String("a") != "a" || !*Bool(true) {
		t.Fatal("pointer helpers")
	}
	e := &Error{Kind: KindRateLimit, Message: "slow"}
	if e.Error() != "slow" || e.Unwrap() != nil || e.RetryAfter != nil {
		t.Fatal(e)
	}
}
