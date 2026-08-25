# MyVocal Multi-Context WSS SDK for Go

Official Go SDK for the MyVocal enterprise Multi-Context WebSocket TTS API.

## Requirements

- Go 1.20+

## Local ZIP use

After extracting the official Release ZIP, verify it directly:

```bash
go test ./...
go vet ./...
```

Before the GitHub repository exists, a local application can use a temporary
`replace` directive:

```go
replace github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go => ../myvocal-multi-context-wss-sdk-go-1.0.0
```

## Minimal flow

```go
package main

import (
    "context"
    "os"
    "time"

    multiwss "github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go"
)

func main() {
    options := multiwss.DefaultConnectionOptions(
        os.Getenv("MYVOCAL_VOICE_ID"))
    options.LanguageCode = "en"
    options.IdleTimeout = 120

    client := multiwss.NewClient(
        os.Getenv("MYVOCAL_TTS_API_KEY"),
        options,
        multiwss.ClientCallbacks{})
    if err := client.Connect(context.Background()); err != nil {
        panic(err)
    }
    synthesis, err := client.OpenContext(
        multiwss.ContextOptions{ContextID: "turn_1"})
    if err != nil {
        panic(err)
    }
    if err := synthesis.SendText("Hello from MyVocal.", false); err != nil {
        panic(err)
    }
    wait, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    if err := synthesis.Finish(wait); err != nil {
        panic(err)
    }
    if err := client.CloseSocket(wait); err != nil {
        panic(err)
    }
}
```

Normal completion is `flush -> close_context -> is_final`. Calling
`close_context` without `flush` is cancellation and does not implicitly flush
buffered text. An empty final audio field is a valid completion frame.

Callbacks run outside the WebSocket reader loop. Callback panics are isolated
and reported through `OnCallbackError`. The SDK serializes writes and never
reconnects or replays text automatically.
