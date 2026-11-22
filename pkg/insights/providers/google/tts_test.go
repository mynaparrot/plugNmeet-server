package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

func TestResolveTTSSettings(t *testing.T) {
	tests := []struct {
		name      string
		options   map[string]interface{}
		voice     string
		wantModel string
		wantVoice string
		wantRate  int
	}{
		{
			name:      "uses the documented defaults",
			wantModel: defaultTTSModel,
			wantVoice: defaultTTSVoice,
			wantRate:  defaultTTSSampleRate,
		},
		{
			name:      "prefers the requested voice",
			options:   map[string]interface{}{"default_voice": "Puck"},
			voice:     "Aoede",
			wantModel: defaultTTSModel,
			wantVoice: "Aoede",
			wantRate:  defaultTTSSampleRate,
		},
		{
			name:      "falls back to the service default voice",
			options:   map[string]interface{}{"default_voice": "Puck"},
			wantModel: defaultTTSModel,
			wantVoice: "Puck",
			wantRate:  defaultTTSSampleRate,
		},
		{
			name:      "keeps a supported sample rate",
			options:   map[string]interface{}{"tts_sample_rate": "24000"},
			wantModel: defaultTTSModel,
			wantVoice: defaultTTSVoice,
			wantRate:  24000,
		},
		{
			name:      "resolves the configured model",
			options:   map[string]interface{}{"model": "gemini-3.8-flash-lite-tts"},
			wantModel: "gemini-3.8-flash-lite-tts",
			wantVoice: defaultTTSVoice,
			wantRate:  defaultTTSSampleRate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var service *config.ServiceConfig
			if tt.options != nil {
				service = &config.ServiceConfig{Options: tt.options}
			}

			model, voice, rate := resolveTTSSettings(service, &insights.SynthesisTaskOptions{Voice: tt.voice})
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
			if voice != tt.wantVoice {
				t.Errorf("voice = %q, want %q", voice, tt.wantVoice)
			}
			if rate != tt.wantRate {
				t.Errorf("sample rate = %d, want %d", rate, tt.wantRate)
			}
		})
	}

	if defaultTTSModel != "gemini-3.8-flash-tts" {
		t.Fatalf("default model = %q, want gemini-3.8-flash-tts", defaultTTSModel)
	}
	if defaultTTSVoice != "Kore" {
		t.Fatalf("default voice = %q, want Kore", defaultTTSVoice)
	}
}

func TestResolveTTSSampleRate(t *testing.T) {
	tests := map[int]int{
		8000:  8000,
		16000: 16000,
		24000: 24000,
		44100: defaultTTSSampleRate,
		0:     defaultTTSSampleRate,
	}

	for rate, want := range tests {
		if got := resolveTTSSampleRate(rate); got != want {
			t.Errorf("resolveTTSSampleRate(%d) = %d, want %d", rate, got, want)
		}
	}
}

func TestBuildTTSRequest(t *testing.T) {
	req := buildTTSRequest("gemini-3.8-flash-tts", "Have a wonderful day!", "Kore", 16000)

	body, err := json.Marshal(req.Body)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var payload struct {
		Model        string `json:"model"`
		Input        string `json:"input"`
		ResponseMode struct {
			Type       string `json:"type"`
			MimeType   string `json:"mime_type"`
			SampleRate int    `json:"sample_rate"`
		} `json:"response_format"`
		GenerationConfig struct {
			SpeechConfig []struct {
				Voice string `json:"voice"`
			} `json:"speech_config"`
		} `json:"generation_config"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v: %s", err, body)
	}

	if payload.Model != "gemini-3.8-flash-tts" {
		t.Errorf("model = %q", payload.Model)
	}
	if payload.Input != "Have a wonderful day!" {
		t.Errorf("input = %q", payload.Input)
	}
	if payload.ResponseMode.Type != "audio" {
		t.Errorf("response format type = %q, want audio", payload.ResponseMode.Type)
	}
	if payload.ResponseMode.MimeType != string(interactions.AudioResponseFormatMimeTypeAudioL16) {
		t.Errorf("mime type = %q, want audio/l16", payload.ResponseMode.MimeType)
	}
	if payload.ResponseMode.SampleRate != 16000 {
		t.Errorf("sample rate = %d, want 16000", payload.ResponseMode.SampleRate)
	}
	if len(payload.GenerationConfig.SpeechConfig) != 1 || payload.GenerationConfig.SpeechConfig[0].Voice != "Kore" {
		t.Errorf("speech config = %+v", payload.GenerationConfig.SpeechConfig)
	}
}

func TestDecodeTTSAudio(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0xFE, 0xFF}
	encoded := base64.StdEncoding.EncodeToString(pcm)

	t.Run("decodes the base64 audio", func(t *testing.T) {
		response := &operations.CreateInteractionResponse{
			Interaction: &interactions.Interaction{
				OutputAudio: &interactions.AudioContent{Data: genai.Ptr(encoded)},
			},
		}

		got, err := decodeTTSAudio(response)
		if err != nil {
			t.Fatalf("decodeTTSAudio() error = %v", err)
		}
		if string(got) != string(pcm) {
			t.Fatalf("audio = %v, want %v", got, pcm)
		}
	})

	t.Run("reports a missing interaction", func(t *testing.T) {
		if _, err := decodeTTSAudio(nil); err == nil {
			t.Fatal("decodeTTSAudio(nil) error = nil, want an error")
		}
	})

	t.Run("reports missing audio", func(t *testing.T) {
		response := &operations.CreateInteractionResponse{Interaction: &interactions.Interaction{}}
		if _, err := decodeTTSAudio(response); err == nil {
			t.Fatal("decodeTTSAudio() error = nil, want a missing audio error")
		}
	})

	t.Run("reports an empty payload", func(t *testing.T) {
		response := &operations.CreateInteractionResponse{
			Interaction: &interactions.Interaction{
				OutputAudio: &interactions.AudioContent{Data: genai.Ptr(base64.StdEncoding.EncodeToString(nil))},
			},
		}
		if _, err := decodeTTSAudio(response); err == nil {
			t.Fatal("decodeTTSAudio() error = nil, want an empty audio error")
		}
	})

	t.Run("reports invalid base64", func(t *testing.T) {
		response := &operations.CreateInteractionResponse{
			Interaction: &interactions.Interaction{
				OutputAudio: &interactions.AudioContent{Data: genai.Ptr("not base64!")},
			},
		}
		if _, err := decodeTTSAudio(response); err == nil {
			t.Fatal("decodeTTSAudio() error = nil, want a decode error")
		}
	})
}

func TestSynthesizeTextRequiresText(t *testing.T) {
	if _, err := synthesizeText(context.Background(), nil, nil, &insights.SynthesisTaskOptions{Text: "   "}); err == nil {
		t.Fatal("synthesizeText() error = nil, want a validation error")
	}
}
