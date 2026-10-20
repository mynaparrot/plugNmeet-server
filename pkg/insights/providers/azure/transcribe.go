package azure

import (
	"context"
	"fmt"

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
		log:   log.WithField("service", "azure-transcribe"),
	}, nil
}

func (c *transcribeClient) CreateTranscription(mainCtx context.Context, roomId, userId string, opts *insights.TranscriptionOptions) (insights.TranscriptionStream, error) {
	log := c.log.WithFields(logrus.Fields{
		"method":     "CreateTranscription",
		"roomId":     roomId,
		"userId":     userId,
		"lang":       opts.SpokenLang,
		"transLangs": opts.TransLangs,
		"storage":    opts.AllowedTranscriptionStorage,
	})
	log.Infoln("starting transcription")

	var (
		inputStream *audio.PushAudioInputStream
		audioConfig *audio.AudioConfig
		cnf         *speech.SpeechTranslationConfig
		recognizer  *speech.TranslationRecognizer
		releaseAll  = func() {
			if recognizer != nil {
				recognizer.Close()
			}
			if audioConfig != nil {
				audioConfig.Close()
			}
			if cnf != nil {
				cnf.Close()
			}
			if inputStream != nil {
				inputStream.Close()
			}
		}
	)

	audioFormat, err := audio.GetWaveFormatPCM(16000, 16, 1)
	if err != nil {
		return nil, fmt.Errorf("could not create audio format: %v", err)
	}

	inputStream, err = audio.CreatePushAudioInputStreamFromFormat(audioFormat)
	// The push stream copies the format, so it can be released here.
	audioFormat.Close()
	if err != nil {
		return nil, fmt.Errorf("could not create audio config from custom inputStream: %v", err)
	}

	audioConfig, err = audio.NewAudioConfigFromStreamInput(inputStream)
	if err != nil {
		releaseAll()
		return nil, err
	}

	cnf, err = speech.NewSpeechTranslationConfigFromSubscription(c.creds.APIKey, c.creds.Region)
	if err != nil {
		releaseAll()
		return nil, err
	}

	if err = cnf.SetSpeechRecognitionLanguage(opts.SpokenLang); err != nil {
		releaseAll()
		return nil, err
	}

	for _, lang := range opts.TransLangs {
		if err = cnf.AddTargetLanguage(lang); err != nil {
			releaseAll()
			return nil, err
		}
	}

	recognizer, err = speech.NewTranslationRecognizerFromConfig(cnf, audioConfig)
	if err != nil {
		releaseAll()
		return nil, err
	}

	stream := newAzureTranscribeStream(mainCtx, inputStream, audioConfig, cnf, recognizer, log)
	stream.registerHandlers(userId, opts)

	if err = <-recognizer.StartContinuousRecognitionAsync(); err != nil {
		log.WithError(err).Errorln("Error starting Azure recognition")
		// Open the gate without a running recognizer, then let teardown release.
		stream.startFinished(false)
		_ = stream.Close()
		return nil, err
	}
	stream.startFinished(true)

	return stream, nil
}

// registerHandlers forwards SDK callbacks; teardown owns all SDK resources.
func (s *azureTranscribeStream) registerHandlers(userId string, opts *insights.TranscriptionOptions) {
	s.recognizer.SessionStarted(func(e speech.SessionEventArgs) {
		s.log.Infoln("azure transcription started")
		s.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	})

	s.recognizer.SessionStopped(func(e speech.SessionEventArgs) {
		s.log.Infoln("azure transcription stopped")
		s.sendTerminal()
		s.requestStop()
	})

	s.recognizer.Recognizing(func(e speech.TranslationRecognitionEventArgs) {
		if e.Result == nil || !hasText(e.Result.Text) {
			return
		}

		result := &plugnmeet.InsightsTranscriptionResult{
			FromUserId:                  userId,
			FromUserName:                opts.UserName,
			Lang:                        GetLocaleFromCode(opts.SpokenLang),
			Text:                        e.Result.Text,
			IsPartial:                   true,
			AllowedTranscriptionStorage: opts.AllowedTranscriptionStorage,
			Translations:                make(map[string]string),
		}
		for lang, text := range e.Result.GetTranslations() {
			result.Translations[GetLocaleFromCode(lang)] = text
		}
		s.send(&insights.TranscriptionEvent{
			Type:   insights.EventTypePartialResult,
			Result: result,
		})
	})

	s.recognizer.Recognized(func(e speech.TranslationRecognitionEventArgs) {
		if e.Result == nil || !hasText(e.Result.Text) {
			// NoMatch results carry no text and must not be published.
			return
		}

		result := &plugnmeet.InsightsTranscriptionResult{
			FromUserId:                  userId,
			FromUserName:                opts.UserName,
			Lang:                        GetLocaleFromCode(opts.SpokenLang),
			Text:                        e.Result.Text,
			IsPartial:                   false,
			AllowedTranscriptionStorage: opts.AllowedTranscriptionStorage,
			Translations:                make(map[string]string),
		}
		for lang, text := range e.Result.GetTranslations() {
			result.Translations[GetLocaleFromCode(lang)] = text
		}
		s.send(&insights.TranscriptionEvent{
			Type:   insights.EventTypeFinalResult,
			Result: result,
		})
	})

	s.recognizer.Canceled(func(e speech.TranslationRecognitionCanceledEventArgs) {
		s.log.Infof("Azure transcription with translation canceled: %v\n", e.ErrorDetails)
		s.send(&insights.TranscriptionEvent{
			Type:  insights.EventTypeError,
			Error: e.ErrorDetails,
		})
		// Report stopped before the blocking teardown so usage can end first.
		s.sendTerminal()
		s.requestStop()
	})
}
