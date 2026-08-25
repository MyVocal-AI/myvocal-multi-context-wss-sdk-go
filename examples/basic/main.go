package main

import (
	"context"
	"fmt"
	"os"
	"time"

	multiwss "github.com/MyVocal-AI/myvocal-multi-context-wss-sdk-go"
)

func main() {
	options := multiwss.DefaultConnectionOptions(
		requireEnvironment("MYVOCAL_VOICE_ID"))
	options.LanguageCode = "en"
	options.IdleTimeout = 120

	client := multiwss.NewClient(
		requireEnvironment("MYVOCAL_TTS_API_KEY"),
		options,
		multiwss.ClientCallbacks{},
	)
	if err := client.Connect(context.Background()); err != nil {
		panic(err)
	}
	synthesis, err := client.OpenContext(multiwss.ContextOptions{
		ContextID: "example",
		Callbacks: multiwss.ContextCallbacks{
			OnAudio: func(audio []byte) {
				fmt.Printf("Audio bytes: %d\n", len(audio))
			},
		},
	})
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

func requireEnvironment(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(name + " is required")
	}
	return value
}
