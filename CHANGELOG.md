# Changelog

## 0.2.1 (2026-09-29)

- Documentation only: the README and the type descriptions no longer name other webhook services. No API changes.

## 0.2.0 (2026-09-29)

- Polling endpoints: `EndpointCreateParams.Type` (`EndpointPolling`, with no `URL`), `Endpoints.ListPollerTokens(ctx, id)`, `Endpoints.CreatePollerToken(ctx, id, *PollerTokenCreateParams)` and `Endpoints.DeletePollerToken(ctx, id, tokenID)`. `NewPoller(endpointID, token, ...Option)` fetches the messages as the receiver with a poller token: `Poll(ctx, *PollParams)`, `Pages` (`*PollIter`) and `Messages` (`*Iter[PolledMessage]`). `EndpointPolling`, `Endpoint.LastPolledAt`, `PollerURL` on `EndpointWithSecret` and `EndpointDetail`, `Endpoint.URL` is empty for a polling endpoint, `DeliveryWaiting` and `Attempt.Via` `"poll"`.
- Destinations other than webhooks: `EndpointCreateParams.Type` and `.Destination` (`URL` is now omitted when empty), `EndpointUpdateParams.Destination`, `Endpoint.Type` and `.Destination`, `Attempt.Ref`, the `EndpointType` constants (`EndpointHTTP`, `DestinationSQS`, …), and `Endpoints.TestDestination(ctx, DestinationTestParams)` for `POST /v1/destinations/test`.
- Transformations: `Endpoints.GetTransformation(ctx, id)`, `Endpoints.SetTransformation(ctx, id, TransformationSetParams)`, `Endpoints.DeleteTransformation(ctx, id)` and `Endpoints.TestTransformation(ctx, id, *TransformationTestParams)` for `/v1/endpoints/:id/transformation`. `Endpoint.TransformationUpdatedAt`, `Attempt.Transform`, and the `cancelled` status on messages and deliveries (`MessageCancelled`, `DeliveryCancelled`, with constants for every `DeliveryStatus`).
- `Ordering` (`OrderingNone`, `OrderingFIFO`) on `Endpoint`, `EndpointCreateParams` and `EndpointUpdateParams`: `OrderingFIFO` sends deliveries to that endpoint one at a time, in the order the messages were accepted. `EndpointDetail.WaitingCount` is the number waiting behind the delivery being sent.
- `EndpointCreateParams.Secret`: import an existing signing secret (`whsec_…`, such as one from another webhook service) instead of generating one.
- `BodyFormat` (`BodyFormatStandard`, `BodyFormatRaw`) on `Endpoint`, `EndpointCreateParams` and `EndpointUpdateParams`: `BodyFormatRaw` sends the message data itself as the body (signed as sent) instead of `{"type", "timestamp", "data"}`.
- `SendMessageParams.TransformationsParams`, passed to transformations as `webhook.transformationsParams`.
- Transformations: `Transformation.Enabled` and `Transformation.Variables` (read as `webhook.env`). `TransformationSetParams` now sends only the fields that are set (`Code`, `Enabled *bool`, `Variables Nullable[map[string]string]`), so `Code` is optional once saved. `TransformationTestParams.Variables` and `.TransformationsParams`. The code limit is now 51,200 characters. `Transformation` is no longer comparable with `==` (it has a map).
- `EndpointUpdateParams.FixedIP`.
- `MessageListParams.Since`: only messages created at or after a time (Unix milliseconds).
- `PollParams.EventTypes` and `PollParams.After`: read only some event types, or start from a time when there is no iterator. `Pages` and `Messages` keep them on every page.

## 0.1.0

- First release: messages, consumers, endpoints (including bulk resend, retry policy and compatibility signature), deliveries, event types, consumer portal links, and webhook signature verification. Same API coverage as the Node.js SDK 0.3.0.
