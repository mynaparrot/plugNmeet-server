package google

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
)

const (
	defaultTTSModel = "gemini-3.8-flash-tts"
	defaultTTSVoice = "Kore"
	// defaultTTSSampleRate matches the publisher the synthesis task creates for
	// providers other than OpenAI when tts_sample_rate is not configured.
	defaultTTSSampleRate = 16000
)

// allowedTTSSampleRates are the rates the TTS models accept.
var allowedTTSSampleRates = []int{8000, 16000, 24000}

// synthesizeText synthesizes one line and returns raw 16-bit little-endian PCM,
// which is what the audio publisher consumes. The Gemini TTS models detect the
// input language themselves, so only the voice shapes the output.
func synthesizeText(ctx context.Context, client *genai.Client, service *config.ServiceConfig, opts *insights.SynthesisTaskOptions) (io.ReadCloser, error) {
	if opts == nil {
		opts = &insights.SynthesisTaskOptions{}
	}

	text := strings.TrimSpace(opts.Text)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	model, voice, sampleRate := resolveTTSSettings(service, opts)

	response, err := client.Interactions.Create(ctx, buildTTSRequest(model, text, voice, sampleRate))
	if err != nil {
		return nil, fmt.Errorf("failed to execute tts request: %w", err)
	}

	pcm, err := decodeTTSAudio(response)
	if err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(pcm)), nil
}

// resolveTTSSettings resolves the model, the voice and the sample rate. An
// explicitly requested voice wins over the service default.
func resolveTTSSettings(service *config.ServiceConfig, opts *insights.SynthesisTaskOptions) (model, voice string, sampleRate int) {
	model = serviceOptionString(service, "model", defaultTTSModel)

	voice = strings.TrimSpace(opts.Voice)
	if voice == "" {
		voice = serviceOptionString(service, "default_voice", defaultTTSVoice)
	}

	sampleRate = resolveTTSSampleRate(serviceOptionInt(service, "tts_sample_rate", defaultTTSSampleRate))
	return model, voice, sampleRate
}

// buildTTSRequest asks for headerless PCM so the bytes can go straight into the
// audio publisher.
func buildTTSRequest(model, text, voice string, sampleRate int) operations.CreateInteractionRequest {
	responseFormat := interactions.NewResponseFormat(interactions.AudioResponseFormat{
		MimeType:   interactions.AudioResponseFormatMimeTypeAudioL16.ToPointer(),
		SampleRate: genai.Ptr(sampleRate),
		Delivery:   interactions.AudioResponseFormatDeliveryInline.ToPointer(),
	})
	speechConfig := interactions.NewSpeechConfigUnion([]interactions.SpeechConfig{{Voice: genai.Ptr(voice)}})

	return operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(interactions.CreateModelInteraction{
			Model:          interactions.Model(model),
			Input:          genai.Ptr(interactions.NewInteractionsInput(text)),
			ResponseFormat: genai.Ptr(interactions.NewCreateModelInteractionResponseFormat(responseFormat)),
			GenerationConfig: &interactions.GenerationConfig{
				SpeechConfig: &speechConfig,
			},
		}),
	}
}

// decodeTTSAudio decodes the base64 audio the interaction carries.
func decodeTTSAudio(response *operations.CreateInteractionResponse) ([]byte, error) {
	if response == nil || response.Interaction == nil {
		return nil, fmt.Errorf("no audio content found in response")
	}

	audio := response.Interaction.OutputAudio
	if audio == nil || audio.Data == nil || *audio.Data == "" {
		return nil, fmt.Errorf("no audio content found in response")
	}

	pcm, err := base64.StdEncoding.DecodeString(*audio.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode audio content: %w", err)
	}
	if len(pcm) == 0 {
		return nil, fmt.Errorf("empty audio content in response")
	}

	return pcm, nil
}

// resolveTTSSampleRate keeps the request on a supported rate, because the
// publisher is created with the configured one.
func resolveTTSSampleRate(rate int) int {
	for _, allowed := range allowedTTSSampleRates {
		if rate == allowed {
			return rate
		}
	}
	return defaultTTSSampleRate
}
