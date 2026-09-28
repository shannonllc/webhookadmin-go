# webhookadmin-go

Go SDK for [Webhook Admin](https://webhookadmin.com/docs/).

- Standard library only.
- Go 1.21+. Every call takes a `context.Context`.

## Install

```sh
go get github.com/shannonllc/webhookadmin-go
```

```go
import webhookadmin "github.com/shannonllc/webhookadmin-go"
```

## Send a message

```go
client, err := webhookadmin.NewClient("sk_live_...") // or set WEBHOOK_ADMIN_API_KEY
if err != nil {
	log.Fatal(err)
}

res, err := client.Messages.Send(ctx, webhookadmin.SendMessageParams{
	Consumer:  "cus_123",
	EventType: "invoice.paid",
	Payload:   map[string]any{"invoice_id": "inv_1", "amount": 1200},
}, webhookadmin.WithIdempotencyKey("invoice-inv_1-paid"))
```

`Consumer` is your customer's ID (`external_id`). The consumer is created if it does not exist. `Payload` is any value that encodes to JSON.

## Register event types

Register the event types you send, so they appear in the dashboard and the consumer portal. `Ensure` creates the missing ones and leaves existing ones unchanged, so it can run on every deploy. Requires the `endpoints:write` scope.

```go
result, err := client.EventTypes.Ensure(ctx, []webhookadmin.EventTypeCreateParams{
	{Name: "invoice.paid", Description: "An invoice was paid"},
	{Name: "invoice.refunded"},
})
```

`EventTypes.Create` returns an error matching `ErrConflict` when the name already exists. `EventTypes.List` returns every event type (not paged), sorted by name. It works with any API key of the environment.

## Verify incoming webhooks

Pass the raw request body, exactly as received.

```go
wh, err := webhookadmin.NewWebhook(os.Getenv("WEBHOOK_SECRET")) // whsec_...

http.HandleFunc("/webhooks", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	event, err := wh.Verify(body, r.Header) // event.Type, event.Timestamp, event.Data (json.RawMessage)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// handle event
	w.WriteHeader(http.StatusNoContent)
})
```

`Verify` returns an error matching `ErrWebhookVerification` when the signature does not match or the timestamp is more than 5 minutes off (`NewWebhook(secret, webhookadmin.WithTolerance(5*time.Minute))`). While a secret is being rotated, messages carry two signatures and either secret verifies. `Sign` computes a signature, for tests.

## API

| Method | Endpoint |
|---|---|
| `Messages.Send(ctx, SendMessageParams, WithIdempotencyKey?)` | `POST /v1/messages` |
| `Messages.List(ctx, *MessageListParams)` / `Messages.ListAll(*MessageListParams)` | `GET /v1/messages` |
| `Messages.Get(ctx, id)` | `GET /v1/messages/:id` |
| `Deliveries.Retry(ctx, id)` | `POST /v1/deliveries/:id/retry` |
| `Consumers.Create(ctx, ConsumerCreateParams)` | `POST /v1/consumers` |
| `Consumers.List(ctx, *ConsumerListParams)` / `Consumers.ListAll(*ConsumerListParams)` | `GET /v1/consumers` |
| `Endpoints.Create(ctx, EndpointCreateParams)` | `POST /v1/endpoints` |
| `Endpoints.List(ctx, *EndpointListParams)` | `GET /v1/endpoints` |
| `Endpoints.Get(ctx, id)` | `GET /v1/endpoints/:id` |
| `Endpoints.Update(ctx, id, EndpointUpdateParams)` | `PATCH /v1/endpoints/:id` |
| `Endpoints.Delete(ctx, id)` | `DELETE /v1/endpoints/:id` |
| `Endpoints.RotateSecret(ctx, id)` | `POST /v1/endpoints/:id/rotate-secret` |
| `Endpoints.SendTest(ctx, id, *EndpointSendTestParams)` | `POST /v1/endpoints/:id/test` |
| `Endpoints.Recover(ctx, id, RecoverParams)` | `POST /v1/endpoints/:id/recover` |
| `Endpoints.Recovery(ctx, id, recoveryID)` | `GET /v1/endpoints/:id/recoveries/:rid` |
| `Portal.CreateLink(ctx, consumerID, *PortalLinkParams)` | `POST /v1/consumers/:id/portal` |
| `EventTypes.List(ctx)` | `GET /v1/event-types` |
| `EventTypes.Create(ctx, EventTypeCreateParams)` | `POST /v1/event-types` |
| `EventTypes.Ensure(ctx, []EventTypeCreateParams)` | `GET` and `POST /v1/event-types` |

Every method also takes request options as trailing arguments: `WithRequestTimeout(d)`, `WithRequestMaxRetries(n)`.

### Updating endpoints

`EndpointUpdateParams` sends only the fields you set. Fields that can be sent as `null` use `Nullable`:

```go
paused := webhookadmin.EndpointPaused
client.Endpoints.Update(ctx, "ep_...", webhookadmin.EndpointUpdateParams{
	Status:     &paused,
	EventTypes: webhookadmin.Null[[]string](),                                        // receive every event type
	Retry:      webhookadmin.Value(webhookadmin.RetryPolicy{Count: 3, Interval: "5m"}), // or Null[RetryPolicy]() for the default
})
```

Set `Status` to `EndpointActive` to resume a paused or disabled endpoint.

### Resend failed deliveries of an endpoint

After a receiver is fixed, resend every delivery to that endpoint that ended `failed`, as the same messages (same `webhook-id`). The endpoint must be `active`.

```go
rec, err := client.Endpoints.Recover(ctx, "ep_...", webhookadmin.RecoverParams{Since: time.Now().Add(-24 * time.Hour).UnixMilli()})
// later
rec, err = client.Endpoints.Recovery(ctx, "ep_...", rec.ID) // rec.Status: "running", "done" or "stopped"
```

Messages created while the endpoint was `disabled` are not covered.

### Retry policy per endpoint

| Field | Values |
|---|---|
| `Count` | `0`, `1`, `2`, `3`, `5`, `7`, `12` (retries after the first send; `0` means no retry) |
| `Interval` | `progressive` (5 s, 5 min, 30 min, 2 h, 5 h, 10 h, 10 h, then 10 h each), `1m`, `5m`, `30m`, `1h`, `6h` |

The default is `{Count: 7, Interval: "progressive"}`: up to 8 attempts including the first, over about 28 hours. Combinations whose last retry comes more than 3 days after the first send (`progressive` with `12`) are rejected with a validation error.

### Compatibility signature

For receivers that still verify a custom signature, add one extra header. The Standard Webhooks headers are always sent. HMAC-SHA256 only; the key is the endpoint's signing secret string (`whsec_...`) as UTF-8. GitHub style:

```go
client.Endpoints.Update(ctx, "ep_...", webhookadmin.EndpointUpdateParams{
	CompatSignature: webhookadmin.Value(webhookadmin.CompatSignature{
		Header: "x-hub-signature-256", Content: "body", Encoding: "hex", Prefix: "sha256=",
	}),
})
```

`Content: "body"` has no replay protection. Use `timestamp_body` (signs `{webhook-timestamp}.{body}`) if you can, and move receivers to `Webhook.Verify`. Send `Null[CompatSignature]()` to remove it.

### Pagination

`List` returns one page (`Items`, `NextCursor`). `ListAll` walks every item across pages:

```go
it := client.Messages.ListAll(&webhookadmin.MessageListParams{Status: webhookadmin.MessageFailed})
for it.Next(ctx) {
	fmt.Println(it.Current().ID)
}
if err := it.Err(); err != nil {
	log.Fatal(err)
}
```

## Errors

Every error is a `*webhookadmin.Error` with `Kind`, `Status`, `Code`, `Message`, `Fields`, `RequestID` and, for 429, `RetryAfter`. Match the kind with `errors.Is`:

| Sentinel | Status |
|---|---|
| `ErrAuthentication` | 401 |
| `ErrPermission` | 403 |
| `ErrNotFound` | 404 |
| `ErrValidation` | 400, 413, 422 |
| `ErrPlanLimit` | 402 |
| `ErrConflict` | 409 |
| `ErrRateLimit` | 429 |
| `ErrAPI` | other, including 5xx |
| `ErrConnection` | no response, or the context was cancelled |
| `ErrTimeout` | no response within the timeout (also matches `ErrConnection`) |

```go
_, err := client.Endpoints.Create(ctx, webhookadmin.EndpointCreateParams{ConsumerID: "con_...", URL: "http://example.com"})
var e *webhookadmin.Error
if errors.As(err, &e) && errors.Is(err, webhookadmin.ErrValidation) {
	fmt.Println(e.Fields) // map[url:...]
}
```

## Retries

| Response | Retried |
|---|---|
| 429 | Always. Waits for `retry-after`, or until `x-ratelimit-reset`. |
| 5xx, connection error, timeout | Only requests that are safe to repeat: `GET`, `PATCH`, `DELETE`, `Messages.Send`, `Portal.CreateLink` |
| Other 4xx | Never |

Backoff starts at 0.5 s, doubles up to 8 s, with ±25% jitter. `Messages.Send` generates an `Idempotency-Key` when you omit it and sends the same key on every retry, so a retried send is delivered once. Keys are kept for 24 hours. Cancelling the context stops the request and the retries.

## Configuration

```go
client, err := webhookadmin.NewClient("sk_live_...",
	webhookadmin.WithBaseURL("https://api.webhookadmin.com"), // default
	webhookadmin.WithTimeout(30*time.Second),                 // per attempt, default 30 s
	webhookadmin.WithMaxRetries(2),                           // default 2
	webhookadmin.WithHTTPClient(&http.Client{}),              // default: a new http.Client
)
```

| Environment variable | Used when |
|---|---|
| `WEBHOOK_ADMIN_API_KEY` | the API key argument is empty |

## License

MIT
