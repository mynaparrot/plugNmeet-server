package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// transcribeClient holds the actual Azure SDK configuration and objects.
type transcribeClient struct {
	creds *config.CredentialsConfig
	model string
	log   *logrus.Entry
}

// newTranscribeClient creates a new client.
func newTranscribeClient(creds *config.CredentialsConfig, model string, log *logrus.Entry) (*transcribeClient, error) {
	if creds.APIKey == "" || creds.Region == "" {
		return nil, fmt.Errorf("azure provider requires api_key (subscription key) and region")
	}

	return &transcribeClient{
		creds: creds,
		model: model,
		log:   log,
	}, nil
}

func (c *transcribeClient) CreateTranscription(mainCtx context.Context, roomId, userId, spokenLang string, transLangs []string) (insights.TranscriptionStream, error) {
	log := c.log.WithFields(logrus.Fields{
		"method": "CreateTranscription",
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

	cnf, err := speech.NewSpeechTranslationConfigFromSubscription(c.creds.APIKey, c.creds.Region)
	if err != nil {
		return nil, err
	}

	err = cnf.SetSpeechRecognitionLanguage(spokenLang)
	if err != nil {
		return nil, err
	}

	for _, lang := range transLangs {
		err := cnf.AddTargetLanguage(lang)
		if err != nil {
			return nil, err
		}
	}

	recognizer, err := speech.NewTranslationRecognizerFromConfig(cnf, audioConfig)
	if err != nil {
		return nil, err
	}

	resultsChan := make(chan *insights.TranscriptionEvent)
	var closeOnce sync.Once
	safeClose := func() {
		closeOnce.Do(func() {
			close(resultsChan)
		})
	}

	recognizer.SessionStarted(func(e speech.SessionEventArgs) {
		log.Infoln("azure transcription started")
		resultsChan <- &insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted}
	})
	recognizer.SessionStopped(func(e speech.SessionEventArgs) {
		log.Infoln("azure transcription stopped")
		resultsChan <- &insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped}
		safeClose()
	})

	recognizer.Recognizing(func(e speech.TranslationRecognitionEventArgs) {
		result := &plugnmeet.InsightsTranscriptionResult{
			FromUserId:   userId,
			Lang:         GetLocaleFromCode(spokenLang),
			Text:         e.Result.Text,
			IsPartial:    true,
			Translations: make(map[string]string),
		}
		for lang, text := range e.Result.GetTranslations() {
			result.Translations[GetLocaleFromCode(lang)] = text
		}
		resultsChan <- &insights.TranscriptionEvent{
			Type:   insights.EventTypePartialResult,
			Result: result,
		}
	})

	recognizer.Recognized(func(e speech.TranslationRecognitionEventArgs) {
		result := &plugnmeet.InsightsTranscriptionResult{
			FromUserId:   userId,
			Lang:         GetLocaleFromCode(spokenLang),
			Text:         e.Result.Text,
			IsPartial:    false,
			Translations: make(map[string]string),
		}
		for lang, text := range e.Result.GetTranslations() {
			result.Translations[GetLocaleFromCode(lang)] = text
		}
		resultsChan <- &insights.TranscriptionEvent{
			Type:   insights.EventTypeFinalResult,
			Result: result,
		}
	})

	recognizer.Canceled(func(e speech.TranslationRecognitionCanceledEventArgs) {
		log.Infof("Azure transcription with translation canceled: %v\n", e.ErrorDetails)
		resultsChan <- &insights.TranscriptionEvent{
			Type:  insights.EventTypeError,
			Error: e.ErrorDetails,
		}
		safeClose()
	})

	err = <-recognizer.StartContinuousRecognitionAsync()
	if err != nil {
		log.WithError(err).Errorln("Error starting Azure recognition")
		safeClose()
	}

	ctx, cancel := context.WithCancel(mainCtx)
	go func() {
		<-ctx.Done()
		recognizer.StopContinuousRecognitionAsync()
	}()

	stream := &azureTranscribeStream{
		pushStream: inputStream,
		cancel:     cancel,
		results:    resultsChan,
	}

	return stream, nil
}
