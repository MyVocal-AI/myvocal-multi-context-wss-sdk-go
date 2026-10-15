# Multi-Context TTS usage guide

Start with the installation and example commands in [README](README.md).

## Connect

Use an enterprise TTS API Key with dedicated concurrency and a voice ID available to your account. Authentication uses the `api-key` handshake header. Keep the Key in server-side configuration or an environment variable; do not put it in a URL, public source code, or logs.

```text
wss://api.voicelibrary.co/enterprise/v1/tts/{voice_id}/websocket/multi
```

Supported models are `flash_v2_5` and `multilingual_v2`. These ordinary TTS WebSocket SDKs do not support `myvocal_v4` or `myvocal_v3_turbo`; use the [dialogue SDKs](https://api-docs.myvocal.ai/3892090w0) for those models.

## Connection options

| Purpose | Python | Java builder | Go |
| --- | --- | --- | --- |
| Model | `model_id` | `modelId(...)` | `ModelID` |
| Audio format | `format` | `format(...)` | `Format` |
| Language | `language_code` | `languageCode(...)` | `LanguageCode` |
| Idle timeout, seconds | `idle_timeout` | `idleTimeout(...)` | `IdleTimeout` |
| Timestamps | `timestamps` | `timestamps(...)` | `Timestamps` |
| Direct streaming | `direct_streaming` | `directStreaming(...)` | `DirectStreaming` |
| Seed | `seed` | `seed(...)` | `Seed` |
| Automatic generation | `auto_mode` | `autoMode(...)` | `AutoMode` |

Choose a model and audio format before connecting. Supported format values are `mp3_44100_64`, `mp3_44100_96`, `mp3_44100_128`, `mp3_44100_192`, `pcm_8000`, `pcm_16000`, `pcm_22050`, `pcm_24000`, `pcm_44100`, and `ulaw_8000`. The examples use `flash_v2_5` and `pcm_16000`; configure your player or output file for the selected encoding and sample rate.

`idleTimeout` accepts `1..180` seconds. `seed` accepts `0..4294967295`; a fixed seed does not guarantee identical audio. Language and advanced option availability depend on the model; consult the [API reference](https://api-docs.myvocal.ai/3758588w0).

The endpoint uses `priority=dedicated_concurrency`. The request audio parameter is `format`; use the language-specific option in the table above.

Multi-Context defaults are `flash_v2_5`, `pcm_16000`, and a 20-second idle timeout. In Go, start with `DefaultConnectionOptions(voiceID)`. The `output_format` / `outputFormat` / `OutputFormat` alias is also accepted; do not supply conflicting format values.

## Create contexts and send text

One connection uses one voice and supports up to five active contexts. Give each active context a unique, non-empty ID. Set voice settings and optional `previous_text`, `next_text`, or `generation_config` when initializing a context. Voice settings `stability`, `similarity_boost`, and `style` accept `0..1`; `use_speaker_boost` is boolean.

Python `create_context(...)` and Go `OpenContext(...)` initialize the context. In Java, call `createContext(...)`, attach callbacks, then call `initialize()` before sending text.

## Finish or cancel a context

Normal completion is:

```text
send text -> flush -> close context -> wait for is_final
```

| Action | Python context | Java context | Go context |
| --- | --- | --- | --- |
| Add text | `send_text(...)` | `sendText(...)` | `SendText(...)` |
| Generate buffered text now | `flush()` | `flush()` | `Flush()` |
| Complete normally | `finish(timeout=60)` | `flush(); close(); awaitFinal(60, TimeUnit.SECONDS)` | `Finish(ctx)` |
| Cancel without flushing | `cancel()` | `close()` without a preceding flush | `Cancel()` |
| Wait for final separately | `wait_final(timeout=60)` | `awaitFinal(60, TimeUnit.SECONDS)` | `WaitFinal(ctx)` |

Python `finish` and Go `Finish` perform flush, context closure, and final waiting. Closing a context alone does not flush pending text: use that path for interruption or cancellation. Cancellation is not successful synthesis. Do not send further text to a completed or cancelled context.

An `is_final=true` event may have an empty or missing audio field; it still signals context completion. Receiving audio alone does not establish completion. A context error must still be handled even if the connection later closes normally.

Finish each context you want to keep before closing the connection with Python `close_socket()`, Java `disconnect()`, or Go `CloseSocket(ctx)`. Connection closure is not a substitute for finishing pending contexts.

## Receive audio

Register a callback per context: Python `on_audio`, Java `onDecodedAudio`, or Go `ContextCallbacks.OnAudio`. These callbacks receive decoded bytes. Save or play chunks in arrival order, separately for each context. Java `onAudio` receives the Base64 audio field; use `onDecodedAudio` when you need bytes.

Python's supplied basic example demonstrates completion without saving audio. To receive audio, pass a callback when creating the context:

```python
context = await client.create_context(
    "turn_1",
    on_audio=lambda audio: print("Audio bytes:", len(audio)),
)
```

The Java and Go examples report audio byte counts. Timestamp callbacks provide alignment data when enabled. Keep context IDs associated with your application output so simultaneous responses are not mixed.

## Errors, timeouts, and retries

Keep callbacks short and avoid waiting for blocking SDK operations inside a callback. Move slow file processing or playback to your application. Handle callback errors separately from connection or synthesis errors.

- For validation errors, check model, format, language, option ranges, and call order.
- For authentication errors, check the enterprise Key and account access.
- For server errors, inspect the available error code, message, and context ID.
- For transport errors or timeouts before completion, clean up the affected connection and decide whether to start a new request.

Python wait timeouts are in seconds. Java wait methods take an explicit `TimeUnit`. Go blocking methods use `context.Context` deadlines. Set a timeout appropriate for the text length; a timeout is not a completion signal.

The SDK does not automatically reconnect or replay text. Retrying completed or partly processed text may produce duplicate audio and additional character usage.

## Usage and support

Use [Query primary key usage](https://api-docs.myvocal.ai/336818975e0) to view character usage. For support, provide the SDK version, operation, model, and error code. Do not include API Keys or private audio/text in public logs.
