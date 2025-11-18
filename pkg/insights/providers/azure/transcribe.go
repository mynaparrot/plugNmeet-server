package azure

import (
	"context"
	"fmt"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// transcribeClient holds the actual Azure SDK configuration and objects.
type transcribeClient struct {
	config *speech.SpeechConfig
	model  string
	log    *logrus.Entry
}

// newTranscribeClient creates a new client.
func newTranscribeClient(creds config.CredentialsConfig, model string, log *logrus.Entry) (*transcribeClient, error) {
	if creds.APIKey == "" || creds.Region == "" {
		return nil, fmt.Errorf("azure provider requires api_key (subscription key) and region")
	}

	cnf, err := speech.NewSpeechConfigFromSubscription(creds.APIKey, creds.Region)
	if err != nil {
		return nil, err
	}

	return &transcribeClient{
		config: cnf,
		model:  model,
		log:    log,
	}, nil
}

func (c *transcribeClient) CreateTranscription(ctx context.Context, roomId, userId, spokenLang string) (insights.TranscriptionStream, error) {
	log := c.log.WithFields(logrus.Fields{
		"method": "TranscribeStream",
		"roomId": roomId,
		"userId": userId,
	})
	log.Infoln("starting transcription")

	audioFormat, err := audio.GetWaveFormatPCM(16000, 16, 1)
	if err != nil {
		return nil, fmt.Errorf("could not create audio format: %v", err)
	}

	inputStream, err := audio.CreatePushAudioInputStreamFromFormat(audioFormat)
	if err != nil {
		return nil, fmt.Errorf("could not create audio config from custom inputStream: %v", err)
	}

	audioConfig, err := audio.NewAudioConfigFromStreamInput(inputStream)
	if err != nil {
		return nil, err
	}

	recognizer, err := speech.NewSpeechRecognizerFromConfig(c.config, audioConfig)
	if err != nil {
		return nil, err
	}

	err = c.config.SetSpeechRecognitionLanguage(spokenLang)
	if err != nil {
		return nil, err
	}
	resultsChan := make(chan *insights.TranscriptionResult)

	recognizer.SessionStarted(func(e speech.SessionEventArgs) {
		log.Infoln("azure transcription started")
	})
	recognizer.SessionStopped(func(e speech.SessionEventArgs) {
		close(resultsChan)
		log.Infoln("azure transcription stopped")
	})

	recognizer.Recognizing(func(e speech.SpeechRecognitionEventArgs) {
		result := &insights.TranscriptionResult{
			Text:      e.Result.Text,
			IsPartial: true,
		}
		resultsChan <- result
	})

	recognizer.Recognized(func(e speech.SpeechRecognitionEventArgs) {
		finalSourceResult := &insights.TranscriptionResult{
			Text:      e.Result.Text,
			IsPartial: false,
		}
		resultsChan <- finalSourceResult

		/*for lang, text := range e.Result.Translations {
			translationResult := &insights.TranscriptionResult{
				Text:      fmt.Sprintf("%s: %s", lang, text),
				IsPartial: false,
			}
			resultsChan <- translationResult
		}*/
	})

	recognizer.Canceled(func(e speech.SpeechRecognitionCanceledEventArgs) {
		log.Infof("Azure transcription canceled: %v\n", e.ErrorDetails)
		close(resultsChan)
	})

	go func() {
		// StartContinuousRecognitionAsync returns a channel that provides the result of the async operation.
		// We must wait for and check the error from this channel.
		err := <-recognizer.StartContinuousRecognitionAsync()
		if err != nil {
			log.WithError(err).Errorln("Error starting Azure recognition")
			close(resultsChan)
		}
	}()

	go func() {
		<-ctx.Done()
		recognizer.StopContinuousRecognitionAsync()
	}()

	stream := &azureTranscribeStream{
		pushStream: inputStream,
		recognizer: recognizer,
		results:    resultsChan,
	}

	return stream, nil
}
