package myvocalmultiwss

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const DefaultBaseURL = "wss://api.voicelibrary.co"

var supportedFormats = map[string]struct{}{
	"mp3_44100_64":  {},
	"mp3_44100_96":  {},
	"mp3_44100_128": {},
	"mp3_44100_192": {},
	"pcm_8000":      {},
	"pcm_16000":     {},
	"pcm_22050":     {},
	"pcm_24000":     {},
	"pcm_44100":     {},
	"ulaw_8000":     {},
}

var supportedModels = map[string]struct{}{
	"flash_v2_5":      {},
	"multilingual_v2": {},
}

var languagePattern = regexp.MustCompile(`^[a-z]{2,3}$`)

type ConnectionOptions struct {
	BaseURL         string
	VoiceID         string
	Priority        string
	ModelID         string
	Format          string
	OutputFormat    string
	LanguageCode    string
	IdleTimeout     int
	DirectStreaming *bool
	Timestamps      *bool
	Seed            *uint64
	AutoMode        *bool
}

func DefaultConnectionOptions(voiceID string) ConnectionOptions {
	return ConnectionOptions{
		BaseURL:     DefaultBaseURL,
		VoiceID:     voiceID,
		Priority:    "dedicated_concurrency",
		ModelID:     "flash_v2_5",
		Format:      "pcm_16000",
		IdleTimeout: 20,
	}
}

func (options ConnectionOptions) effectiveFormat() (string, error) {
	if options.Format != "" && options.OutputFormat != "" &&
		options.Format != options.OutputFormat {
		return "", &ValidationError{
			Message: "format conflicts with output_format",
		}
	}
	if options.Format != "" {
		return options.Format, nil
	}
	if options.OutputFormat != "" {
		return options.OutputFormat, nil
	}
	return "pcm_16000", nil
}

func (options ConnectionOptions) Validate() error {
	if options.BaseURL == "" {
		return &ValidationError{Message: "base_url must not be empty"}
	}
	if options.VoiceID == "" {
		return &ValidationError{Message: "voice_id must not be empty"}
	}
	if options.Priority != "dedicated_concurrency" {
		return &ValidationError{
			Message: "priority must be dedicated_concurrency",
		}
	}
	if _, ok := supportedModels[options.ModelID]; !ok {
		return &ValidationError{Message: "unsupported model_id"}
	}
	format, err := options.effectiveFormat()
	if err != nil {
		return err
	}
	if _, ok := supportedFormats[format]; !ok {
		return &ValidationError{Message: "unsupported format"}
	}
	if options.LanguageCode != "" &&
		!languagePattern.MatchString(options.LanguageCode) {
		return &ValidationError{
			Message: "language_code must be 2-3 lowercase ASCII letters",
		}
	}
	if options.IdleTimeout < 1 || options.IdleTimeout > 180 {
		return &ValidationError{
			Message: "idle_timeout must be between 1 and 180",
		}
	}
	parsed, err := url.Parse(options.BaseURL)
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" {
		return &ValidationError{
			Message: "base_url must be an absolute wss URL",
		}
	}
	return nil
}

func (options ConnectionOptions) BuildURL() (string, error) {
	if err := options.Validate(); err != nil {
		return "", err
	}
	parsed, _ := url.Parse(strings.TrimRight(options.BaseURL, "/"))
	parsed.Path = strings.TrimRight(parsed.Path, "/") +
		"/enterprise/v1/tts/" + url.PathEscape(options.VoiceID) +
		"/websocket/multi"
	query := parsed.Query()
	format, _ := options.effectiveFormat()
	query.Set("priority", options.Priority)
	query.Set("model_id", options.ModelID)
	query.Set("format", format)
	query.Set("idleTimeout", strconv.Itoa(options.IdleTimeout))
	if options.LanguageCode != "" {
		query.Set("language_code", options.LanguageCode)
	}
	if options.DirectStreaming != nil {
		query.Set("directStreaming",
			strconv.FormatBool(*options.DirectStreaming))
	}
	if options.Timestamps != nil {
		query.Set("Timestamps", strconv.FormatBool(*options.Timestamps))
	}
	if options.Seed != nil {
		query.Set("seed", strconv.FormatUint(*options.Seed, 10))
	}
	if options.AutoMode != nil {
		query.Set("auto_mode", strconv.FormatBool(*options.AutoMode))
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type VoiceSettings struct {
	Stability       *float64
	SimilarityBoost *float64
	Style           *float64
	UseSpeakerBoost *bool
}

func (settings VoiceSettings) Validate() error {
	values := map[string]*float64{
		"stability":        settings.Stability,
		"similarity_boost": settings.SimilarityBoost,
		"style":            settings.Style,
	}
	for name, value := range values {
		if value != nil && (*value < 0 || *value > 1) {
			return &ValidationError{
				Message: name + " must be between 0 and 1",
			}
		}
	}
	return nil
}

func (settings VoiceSettings) wire() map[string]any {
	result := map[string]any{}
	if settings.Stability != nil {
		result["stability"] = *settings.Stability
	}
	if settings.SimilarityBoost != nil {
		result["similarity_boost"] = *settings.SimilarityBoost
	}
	if settings.Style != nil {
		result["style"] = *settings.Style
	}
	if settings.UseSpeakerBoost != nil {
		result["use_speaker_boost"] = *settings.UseSpeakerBoost
	}
	return result
}

type ContextCallbacks struct {
	OnAudio      func(audio []byte)
	OnTimestamps func(value any)
	OnComplete   func()
	OnError      func(err error)
}

type ContextOptions struct {
	ContextID        string
	InitialText      string
	PreviousText     string
	NextText         string
	VoiceSettings    *VoiceSettings
	GenerationConfig map[string]any
	Callbacks        ContextCallbacks
}

func (options ContextOptions) wire() (map[string]any, error) {
	if strings.TrimSpace(options.ContextID) == "" {
		return nil, &ValidationError{
			Message: "context_id must not be empty",
		}
	}
	if options.VoiceSettings != nil {
		if err := options.VoiceSettings.Validate(); err != nil {
			return nil, err
		}
	}
	initial := options.InitialText
	if initial == "" {
		initial = " "
	}
	value := map[string]any{
		"context_id": options.ContextID,
		"text":       initial,
	}
	if options.PreviousText != "" {
		value["previous_text"] = options.PreviousText
	}
	if options.NextText != "" {
		value["next_text"] = options.NextText
	}
	if options.VoiceSettings != nil {
		value["voice_settings"] = options.VoiceSettings.wire()
	}
	if options.GenerationConfig != nil {
		value["generation_config"] = options.GenerationConfig
	}
	return value, nil
}

func protocolErrorFrom(event map[string]any) *ProtocolError {
	rawError := event["error"]
	code, _ := rawError.(string)
	if code == "" {
		code = stringValue(event["code"])
	}
	if code == "" {
		code = stringValue(event["error_code"])
	}
	nested, _ := rawError.(map[string]any)
	if code == "" && nested != nil {
		code = stringValue(nested["code"])
	}
	if code == "" {
		return nil
	}
	message := stringValue(event["message"])
	if message == "" {
		message = stringValue(event["error_message"])
	}
	field := stringValue(event["field"])
	if nested != nil {
		if message == "" {
			message = stringValue(nested["message"])
		}
		if field == "" {
			field = stringValue(nested["field"])
		}
	}
	if message == "" {
		message = code
	}
	contextID := stringValue(event["context_id"])
	if contextID == "" {
		contextID = stringValue(event["contextId"])
	}
	return &ProtocolError{
		Code:      code,
		Field:     field,
		Message:   message,
		ContextID: contextID,
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if result, ok := value.(string); ok {
		return result
	}
	return fmt.Sprint(value)
}
