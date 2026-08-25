# MyVocal Enterprise Multi-Context WSS Contract

## Authority

This contract controls the Python, Java, and Go SDKs. Current production
behavior is authoritative. APIFox and historical SDKs are inputs; any field
that has not yet been exercised in the current live validation remains subject
to the final verification gate.

## Connection

The default endpoint is:

```text
wss://api.voicelibrary.co/enterprise/v1/tts/{voice_id}/websocket/multi
```

Authentication uses the `api-key` WebSocket handshake header. Keys are never
accepted as URL query values or logged by the SDK.

The SDK always sends `priority=dedicated_concurrency`. Supported models are
`flash_v2_5` and `multilingual_v2`.

The public SDK option is named `format`. Legacy callers may supply
`output_format`; SDKs normalize it to the `format` wire field. Supplying both
with different values is rejected locally.

`idleTimeout` is measured in seconds, defaults to `20`, and accepts `1..180`.
The legacy name `inactivity_timeout` is not sent.

## Contexts

One socket supports at most five locally active contexts. A context is created
with a non-empty `context_id` and an initialization message. `voice_settings`,
`previous_text`, `next_text`, and `generation_config` belong to initialization.

`voice_settings` is sent on the first context message only unless the caller
explicitly starts a new context with the same ID after the previous one has
reached a terminal state.

## Completion and Barge-in

Normal completion:

```text
send text -> flush -> close_context -> wait for is_final
```

`close_context` is not an implicit flush. For barge-in or cancellation, callers
may close a context without waiting for pending audio and the local slot is
released deterministically.

An `is_final=true` frame is successful even when `audio` is absent, `null`, or
an empty string. The SDK invokes completion exactly once.

## Responses and Errors

Both text JSON frames and binary frames containing JSON are parsed. Direct
binary audio is routed by the active context where the server contract provides
that association.

The SDK accepts `context_id`/`contextId` and `is_final`/`isFinal`. Base64 audio
is decoded before user callbacks. Timestamp/alignment payloads remain
structured data.

The first terminal protocol error is sticky and cannot be overwritten by a
later normal Close frame. A context-scoped terminal error does not terminate
unrelated contexts. A connection-scoped error terminates the socket and all
active contexts.

Callbacks are isolated from the reader. Audio events do not enter the bounded
control queue. The queue holds at most 16 control events and never blocks the
read loop.

The SDK never automatically replays submitted text after reconnection.

## Verification Status

Offline contract and adversarial tests are required before packaging. Current
production verification requires explicit authorization and will persist only
event counts, audio byte counts, in-memory hashes, final counts, close codes,
and pass/fail results.
