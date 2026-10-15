# MyVocal Multi-Context TTS SDK for Go

Requires Go 1.20 or later, an enterprise TTS API Key with dedicated concurrency, and a voice ID. Use one voice with up to five active synthesis contexts on a connection.

## Install

From your application's Go module:

```bash
go get github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go@v1.0.1
```

To use the [downloadable ZIP](https://github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go/releases/tag/v1.0.1), extract it beside your application and add the following to your application's `go.mod`:

```go
require github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go v1.0.1

replace github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go => ../myvocal-multi-context-wss-sdk-go-1.0.1
```

Adjust the relative path to the extracted folder and run `go mod tidy`.

## Models and audio

Supported models: `flash_v2_5` and `multilingual_v2`. The example uses `flash_v2_5` with `pcm_16000`; edit its connection options to use another supported model or format.

`myvocal_v4` and `myvocal_v3_turbo` require the [dialogue SDKs](https://api-docs.myvocal.ai/3892090w0) and are not supported by this endpoint.

## Run the example

Set your Key and voice ID:

```bash
export MYVOCAL_TTS_API_KEY='YOUR_API_KEY'
export MYVOCAL_VOICE_ID='YOUR_VOICE_ID'
```

PowerShell:

```powershell
$env:MYVOCAL_TTS_API_KEY='YOUR_API_KEY'
$env:MYVOCAL_VOICE_ID='YOUR_VOICE_ID'
```

From the extracted SDK folder:

```bash
go run ./examples/basic
```

Running the example makes a real synthesis request and consumes character usage. It reports audio byte counts; add file or playback handling to consume the audio.

## Use in your application

- Normal completion requires flush, context closure, and a final event. Closing a context without flushing is cancellation.
- [Usage guide](USAGE.md): options, audio callbacks, completion, cancellation, timeouts, and errors.
- [API reference](https://api-docs.myvocal.ai/3758588w0): supported fields and messages.
- [Character usage](https://api-docs.myvocal.ai/336818975e0).

The SDK does not automatically reconnect or replay text.

Licensed under [Apache License 2.0](LICENSE). Dependency notices are in [NOTICE](NOTICE).
