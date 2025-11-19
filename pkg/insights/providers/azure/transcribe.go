package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
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

	resultsChan := make(chan *insights.TranscriptionResult)
	var closeOnce sync.Once
	safeClose := func() {
		closeOnce.Do(func() {
			close(resultsChan)
		})
	}

	recognizer.SessionStarted(func(e speech.SessionEventArgs) {
		log.Infoln("azure transcription started")
	})
	recognizer.SessionStopped(func(e speech.SessionEventArgs) {
		safeClose()
		log.Infoln("azure transcription stopped")
	})

	recognizer.Recognizing(func(e speech.TranslationRecognitionEventArgs) {
		result := &insights.TranscriptionResult{
			Lang:         spokenLang,
			Text:         e.Result.Text,
			IsPartial:    true,
			Translations: map[string]string{},
		}

		for lang, text := range e.Result.GetTranslations() {
			result.Translations[lang] = text
		}
		resultsChan <- result
	})

	recognizer.Recognized(func(e speech.TranslationRecognitionEventArgs) {
		result := &insights.TranscriptionResult{
			Lang:         spokenLang,
			Text:         e.Result.Text,
			IsPartial:    false,
			Translations: map[string]string{},
		}

		for lang, text := range e.Result.GetTranslations() {
			result.Translations[lang] = text
		}
		resultsChan <- result
	})

	recognizer.Canceled(func(e speech.TranslationRecognitionCanceledEventArgs) {
		log.Infof("Azure transcription with translation canceled: %v\n", e.ErrorDetails)
		safeClose()
	})

	go func() {
		// StartContinuousRecognitionAsync returns a channel that provides the result of the async operation.
		// We must wait for and check the error from this channel.
		err := <-recognizer.StartContinuousRecognitionAsync()
		if err != nil {
			log.WithError(err).Errorln("Error starting Azure recognition")
			safeClose()
		}
	}()

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
