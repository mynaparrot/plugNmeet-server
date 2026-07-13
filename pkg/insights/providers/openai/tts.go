package openai

import (
	"context"
	"fmt"
	"io"
	"strings"

	sdk "github.com/openai/openai-go/v3"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// synthesizeText performs the synthesis and returns a streaming reader for 16kHz PCM audio.
func synthesizeText(ctx context.Context, client sdk.Client, service *config.ServiceConfig, text, language, voice string) (io.ReadCloser, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}

	model := service.GetOptionsString("model", sdk.SpeechModelGPT4oMiniTTS)
	if voice == "" {
		voice = service.GetOptionsString("default_voice", "alloy")
	}

	params := sdk.AudioSpeechNewParams{
		Model:          model,
		Input:          text,
		ResponseFormat: sdk.AudioSpeechNewParamsResponseFormatPCM,
		Voice: sdk.AudioSpeechNewParamsVoiceUnion{
			OfString: sdk.String(voice),
		},
	}

	if language != "" && strings.HasPrefix(model, "gpt-4o") {
		params.Instructions = sdk.String(fmt.Sprintf(
			"Speak naturally in %s with a normal conversational pace. Avoid long pauses.",
			language,
		))
	}

	res, err := client.Audio.Speech.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to execute tts request: %w", err)
	}

	return res.Body, nil
}
