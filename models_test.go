package myvocalmultiwss

import (
	"net/url"
	"testing"
)

func TestBuildURLUsesCanonicalEnterpriseFields(t *testing.T) {
	enabled := true
	seed := uint64(42)
	options := DefaultConnectionOptions("voice 123")
	options.BaseURL = "wss://api.voicelibrary.co"
	options.Format = "pcm_8000"
	options.LanguageCode = "en"
	options.IdleTimeout = 120
	options.DirectStreaming = &enabled
	options.Timestamps = &enabled
	options.Seed = &seed
	options.AutoMode = &enabled

	raw, err := options.BuildURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/enterprise/v1/tts/voice%20123/websocket/multi" {
		t.Fatalf("unexpected path: %s", parsed.Path)
	}
	query := parsed.Query()
	expected := map[string]string{
		"priority":        "dedicated_concurrency",
		"model_id":        "flash_v2_5",
		"format":          "pcm_8000",
		"language_code":   "en",
		"idleTimeout":     "120",
		"directStreaming": "true",
		"Timestamps":      "true",
		"seed":            "42",
		"auto_mode":       "true",
	}
	for key, value := range expected {
		if query.Get(key) != value {
			t.Fatalf("%s=%q, want %q", key, query.Get(key), value)
		}
	}
	if query.Has("output_format") || query.Has("inactivity_timeout") {
		t.Fatal("legacy query name leaked to wire")
	}
}

func TestOutputFormatAliasNormalizesToFormat(t *testing.T) {
	options := DefaultConnectionOptions("voice")
	options.Format = ""
	options.OutputFormat = "mp3_44100_128"
	raw, err := options.BuildURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(raw)
	if parsed.Query().Get("format") != "mp3_44100_128" {
		t.Fatalf("unexpected format: %s", parsed.Query().Get("format"))
	}
}

func TestConnectionValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ConnectionOptions)
	}{
		{"priority", func(o *ConnectionOptions) { o.Priority = "auto" }},
		{"model", func(o *ConnectionOptions) { o.ModelID = "eleven_v3" }},
		{"format", func(o *ConnectionOptions) { o.Format = "wav" }},
		{"language", func(o *ConnectionOptions) { o.LanguageCode = "EN" }},
		{"idle low", func(o *ConnectionOptions) { o.IdleTimeout = 0 }},
		{"idle high", func(o *ConnectionOptions) { o.IdleTimeout = 181 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultConnectionOptions("voice")
			test.mutate(&options)
			if _, err := options.BuildURL(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestVoiceSettingsRange(t *testing.T) {
	invalid := 1.01
	settings := VoiceSettings{Stability: &invalid}
	if err := settings.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
