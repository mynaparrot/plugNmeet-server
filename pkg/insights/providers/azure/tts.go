package azure

import (
	"context"
	"fmt"
	"io"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/common" // Correct import
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// ttsClient holds the configuration needed for Azure text-to-speech.
type ttsClient struct {
	creds *config.CredentialsConfig
	log   *logrus.Entry
}

// newTTSClient creates a new client for text-to-speech.
func newTTSClient(creds *config.CredentialsConfig, log *logrus.Entry) (*ttsClient, error) {
	if creds.APIKey == "" || creds.Region == "" {
		return nil, fmt.Errorf("azure provider requires api_key (subscription key) and region")
	}

	return &ttsClient{
		creds: creds,
		log:   log.WithField("service", "azure-tts"),
	}, nil
}

// SynthesizeText performs the synthesis and returns a streaming reader for the audio data.
func (c *ttsClient) SynthesizeText(ctx context.Context, text, language, voice string) (io.ReadCloser, error) {
	// Create the speech config from the stored credentials for this specific task.
	conf, err := speech.NewSpeechConfigFromSubscription(c.creds.APIKey, c.creds.Region)
	if err != nil {
		return nil, fmt.Errorf("failed to create azure speech config: %w", err)
	}
	defer conf.Close()

	if language == "zh-Hans" {
		language = "zh-CN"
	} else if language == "zh-Hant" {
		language = "zh-TW"
	}

	err = conf.SetSpeechSynthesisLanguage(language)
	if err != nil {
		return nil, fmt.Errorf("failed to set synthesis language: %w", err)
	}

	if voice != "" {
		err = conf.SetSpeechSynthesisVoiceName(voice)
		if err != nil {
			return nil, fmt.Errorf("failed to set synthesis voice: %w", err)
		}
	}

	// Create the synthesizer. Audio config is nil as we get a stream from the result.
	synthesizer, err := speech.NewSpeechSynthesizerFromConfig(conf, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech synthesizer: %w", err)
	}
	defer synthesizer.Close()

	// Start the synthesis task asynchronously.
	task := synthesizer.StartSpeakingTextAsync(text)
	var outcome speech.SpeechSynthesisOutcome

	// Wait for the synthesis to complete or for the context to be cancelled.
	select {
	case outcome = <-task:
	// Synthesis is complete, proceed.
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled while waiting for synthesis result: %w", ctx.Err())
	}

	// Check for errors in the synthesis outcome.
	if outcome.Error != nil {
		outcome.Close() // Clean up resources on error.
		return nil, fmt.Errorf("synthesis outcome error: %w", outcome.Error)
	}

	// Verify that the synthesis was successful.
	if outcome.Result.Reason != common.SynthesizingAudioStarted {
		cancellation, _ := speech.NewCancellationDetailsFromSpeechSynthesisResult(outcome.Result)
		err := fmt.Errorf("synthesis failed: reason=%s, details=%s", outcome.Result.Reason.String(), cancellation.ErrorDetails)
		outcome.Close() // Clean up resources on error.
		return nil, err
	}

	// Create the audio data stream from the successful result.
	stream, err := speech.NewAudioDataStreamFromSpeechSynthesisResult(outcome.Result)
	if err != nil {
		outcome.Close() // Clean up resources on error.
		return nil, fmt.Errorf("failed to create audio data stream: %w", err)
	}

	// Return our custom wrapper which holds the stream and the outcome,
	// ensuring both are closed properly by the consumer.
	return &azureTTSStream{
		stream:  stream,
		outcome: &outcome,
	}, nil
}
