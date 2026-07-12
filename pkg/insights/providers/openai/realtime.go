package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/sirupsen/logrus"
)

const (
	defaultLiveKitInputSampleRate = 16000
	defaultRealtimeSampleRate     = 24000

	defaultRealtimeTranscriptionModel = "gpt-realtime-whisper"

	defaultTranscriptionCommitCheckIntervalMs = 100
	defaultTranscriptionMinCommitMs           = 1500
	defaultTranscriptionSilenceCommitMs       = 900
	defaultTranscriptionMaxCommitMs           = 12000
	defaultTranscriptionMaxBufferedSilenceMs  = 400
	defaultTranscriptionSpeechRMS             = 500

	transcriptionTurnDetectionManual      = "manual"
	transcriptionTurnDetectionNone        = "none"
	transcriptionTurnDetectionDisabled    = "disabled"
	transcriptionTurnDetectionNull        = "null"
	transcriptionTurnDetectionServerVAD   = "server_vad"
	transcriptionTurnDetectionSemanticVAD = "semantic_vad"
)

type realtimeClient struct {
	account     *config.ProviderAccount
	service     *config.ServiceConfig
	log         *logrus.Entry
	llmProvider *OpenAIProvider
}

func newRealtimeClient(
	account *config.ProviderAccount,
	service *config.ServiceConfig,
	log *logrus.Entry,
	llmProvider *OpenAIProvider,
) (*realtimeClient, error) {
	return &realtimeClient{
		account:     account,
		service:     service,
		log:         log.WithField("service", "openai-realtime"),
		llmProvider: llmProvider,
	}, nil
}

func (c *realtimeClient) CreateTranscription(
	mainCtx context.Context,
	roomId, userId string,
	opts *insights.TranscriptionOptions,
) (insights.TranscriptionStream, error) {
	log := c.log.WithFields(logrus.Fields{
		"method":     "CreateTranscription",
		"roomId":     roomId,
		"userId":     userId,
		"lang":       opts.SpokenLang,
		"transLangs": opts.TransLangs,
	})
	log.Infoln("starting openai realtime transcription")

	baseURL := c.account.GetOptionsString("endpoint", defaultBaseURL)
	wsURL := toWebSocketURL(baseURL)

	ctx, cancel := context.WithCancel(mainCtx)

	stream := newOpenAIRealtimeStream(ctx, cancel, log, opts, c.llmProvider)
	stream.inputSampleRate = getIntOption(c.service, "input_sample_rate", defaultLiveKitInputSampleRate)

	if err := c.createTranscriptionSession(wsURL, stream, opts); err != nil {
		cancel()
		return nil, err
	}

	stream.safeSend(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})

	go func() {
		stream.writeWg.Wait()
		stream.readWg.Wait()
		stream.commitWg.Wait()
		stream.eventWg.Wait()
		close(stream.results)
	}()

	return stream, nil
}

func (c *realtimeClient) createTranscriptionSession(
	wsURL string,
	stream *openaiRealtimeStream,
	opts *insights.TranscriptionOptions,
) error {
	transcriptionModel := c.service.GetOptionsString(
		"realtime_transcription_model",
		defaultRealtimeTranscriptionModel,
	)

	turnDetectionMode := getTranscriptionTurnDetectionMode(c.service)
	manualCommit := shouldUseManualTranscriptionCommit(turnDetectionMode)

	minCommitMs := getIntOption(c.service, "transcription_min_commit_ms", defaultTranscriptionMinCommitMs)
	silenceCommitMs := getIntOption(c.service, "transcription_silence_commit_ms", defaultTranscriptionSilenceCommitMs)
	maxCommitMs := getIntOption(c.service, "transcription_max_commit_ms", defaultTranscriptionMaxCommitMs)
	maxBufferedSilenceMs := getIntOption(c.service, "transcription_max_buffered_silence_ms", defaultTranscriptionMaxBufferedSilenceMs)
	speechRMS := getIntOption(c.service, "transcription_speech_rms", defaultTranscriptionSpeechRMS)

	stream.manualCommit = manualCommit
	stream.transcriptionMinCommitSamples = samplesForDuration(defaultRealtimeSampleRate, minCommitMs)
	stream.transcriptionSilenceCommitDuration = time.Duration(silenceCommitMs) * time.Millisecond
	stream.transcriptionMaxCommitDuration = time.Duration(maxCommitMs) * time.Millisecond
	stream.transcriptionMaxBufferedSilenceSamples = samplesForDuration(defaultRealtimeSampleRate, maxBufferedSilenceMs)
	stream.transcriptionSpeechRMSThreshold = float64(speechRMS)

	authHeader := c.service.GetOptionsString("realtime_auth_header", "Authorization")

	header := http.Header{}
	setOpenAIRealtimeHeaders(header, c.account.Credentials.APIKey, authHeader)

	endpoint := strings.TrimSuffix(wsURL, "/") + "/realtime?intent=transcription"

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(endpoint, header)
	if err != nil {
		return fmt.Errorf("failed to dial openai realtime transcription: %w", err)
	}

	stream.transcriptionSession = &openaiTranscriptionSession{
		conn: conn,
	}

	input := realtime.RealtimeTranscriptionSessionAudioInputParam{
		Format: realtime.RealtimeAudioFormatsUnionParam{
			OfAudioPCM: &realtime.RealtimeAudioFormatsAudioPCMParam{
				Rate: defaultRealtimeSampleRate,
				Type: "audio/pcm",
			},
		},
		Transcription: realtime.AudioTranscriptionParam{
			Model: realtime.AudioTranscriptionModel(transcriptionModel),
		},
	}

	if spokenLang := strings.TrimSpace(opts.SpokenLang); spokenLang != "" {
		input.Transcription.Language = sdk.String(spokenLang)
	}

	if delay := strings.TrimSpace(c.service.GetOptionsString("transcription_delay", "")); delay != "" {
		input.Transcription.Delay = realtime.AudioTranscriptionDelay(delay)
	}

	if !manualCommit {
		switch turnDetectionMode {
		case transcriptionTurnDetectionServerVAD:
			input.TurnDetection = realtime.RealtimeTranscriptionSessionAudioInputTurnDetectionUnionParam{
				OfServerVad: &realtime.RealtimeTranscriptionSessionAudioInputTurnDetectionServerVadParam{
					Type: transcriptionTurnDetectionServerVAD,
				},
			}

		case transcriptionTurnDetectionSemanticVAD:
			input.TurnDetection = realtime.RealtimeTranscriptionSessionAudioInputTurnDetectionUnionParam{
				OfSemanticVad: &realtime.RealtimeTranscriptionSessionAudioInputTurnDetectionSemanticVadParam{
					Type: transcriptionTurnDetectionSemanticVAD,
				},
			}
		}
	}

	session := realtime.RealtimeTranscriptionSessionCreateRequestParam{
		Type: constant.ValueOf[constant.Transcription](),
		Audio: realtime.RealtimeTranscriptionSessionAudioParam{
			Input: input,
		},
	}
	configBytes, err := json.Marshal(map[string]any{
		"type":    constant.ValueOf[constant.SessionUpdate](),
		"session": session,
	})
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to marshal transcription session config: %w", err)
	}

	stream.writeWg.Add(1)
	go stream.writeLoop()

	if err := stream.queueWebSocketMessage(configBytes); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to queue transcription session configuration payload: %w", err)
	}

	stream.log.WithFields(logrus.Fields{
		"transcriptionModel": transcriptionModel,
		"spokenLang":         opts.SpokenLang,
		"transLangs":         opts.TransLangs,
		"turnDetection":      turnDetectionMode,
		"manualCommit":       manualCommit,
	}).Infoln("Created openai realtime transcription session")

	stream.readWg.Add(1)
	go stream.readTranscriptionLoop()

	if manualCommit {
		c.startTranscriptionCommitLoop(stream)
	} else {
		stream.log.WithField("turnDetection", turnDetectionMode).
			Infoln("openai realtime transcription manual commit loop disabled")
	}

	return nil
}

func (c *realtimeClient) startTranscriptionCommitLoop(stream *openaiRealtimeStream) {
	intervalMs := getIntOption(
		c.service,
		"transcription_commit_check_interval_ms",
		defaultTranscriptionCommitCheckIntervalMs,
	)

	if intervalMs <= 0 {
		stream.log.Infoln("openai realtime transcription adaptive commit loop disabled")
		return
	}

	interval := time.Duration(intervalMs) * time.Millisecond

	stream.log.WithFields(logrus.Fields{
		"checkInterval":  interval.String(),
		"minSamples":     stream.transcriptionMinCommitSamples,
		"silenceCommit":  stream.transcriptionSilenceCommitDuration.String(),
		"maxCommit":      stream.transcriptionMaxCommitDuration.String(),
		"speechRMSThres": stream.transcriptionSpeechRMSThreshold,
	}).Infoln("Starting openai realtime adaptive transcription commit loop")

	stream.commitWg.Add(1)
	go func() {
		defer stream.commitWg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stream.ctx.Done():
				return

			case <-ticker.C:
				if stream.closed.Load() {
					return
				}

				if err := maybeCommitTranscriptionBuffer(stream); err != nil {
					if stream.ctx.Err() == nil && !stream.closed.Load() {
						stream.log.WithError(err).Debug("failed to adaptive-commit openai realtime transcription buffer")
					}
				}
			}
		}
	}()
}

func maybeCommitTranscriptionBuffer(stream *openaiRealtimeStream) error {
	if stream.closed.Load() {
		return nil
	}

	stream.mu.Lock()
	defer stream.mu.Unlock()

	if stream.closed.Load() {
		return nil
	}

	if stream.transcriptionSession == nil || stream.transcriptionSession.conn == nil {
		return fmt.Errorf("openai transcription session is not initialized")
	}

	if !stream.manualCommit {
		return nil
	}

	if !stream.transcriptionHasSpeech {
		return nil
	}

	if stream.transcriptionSegmentSamples < stream.transcriptionMinCommitSamples {
		return nil
	}

	now := time.Now()

	silenceDuration := now.Sub(stream.transcriptionLastSpeechAt)
	segmentDuration := now.Sub(stream.transcriptionSegmentStartedAt)

	silenceLimit := stream.transcriptionSilenceCommitDuration
	if silenceLimit <= 0 {
		silenceLimit = time.Duration(defaultTranscriptionSilenceCommitMs) * time.Millisecond
	}

	maxLimit := stream.transcriptionMaxCommitDuration
	if maxLimit <= 0 {
		maxLimit = time.Duration(defaultTranscriptionMaxCommitMs) * time.Millisecond
	}

	shouldCommit := silenceDuration >= silenceLimit || segmentDuration >= maxLimit
	if !shouldCommit {
		return nil
	}

	if err := stream.queueAudioCommit(); err != nil {
		return err
	}

	stream.resetPendingTranscriptionLocked()
	return nil
}

func getTranscriptionTurnDetectionMode(service *config.ServiceConfig) string {
	mode := strings.ToLower(strings.TrimSpace(
		service.GetOptionsString("transcription_turn_detection", ""),
	))

	if mode != "" {
		switch mode {
		case transcriptionTurnDetectionServerVAD,
			transcriptionTurnDetectionSemanticVAD,
			transcriptionTurnDetectionManual,
			transcriptionTurnDetectionNone,
			transcriptionTurnDetectionDisabled,
			transcriptionTurnDetectionNull:
			return mode
		default:
			return transcriptionTurnDetectionManual
		}
	}

	return transcriptionTurnDetectionManual
}

func shouldUseManualTranscriptionCommit(mode string) bool {
	switch mode {
	case transcriptionTurnDetectionServerVAD, transcriptionTurnDetectionSemanticVAD:
		return false
	default:
		return true
	}
}

func setOpenAIRealtimeHeaders(header http.Header, apiKey string, authHeader string) {
	authHeader = strings.TrimSpace(authHeader)
	if authHeader == "" {
		authHeader = "Authorization"
	}

	switch strings.ToLower(authHeader) {
	case "api-key":
		header.Set("api-key", apiKey)

	case "authorization":
		header.Set("Authorization", "Bearer "+apiKey)

	default:
		header.Set(authHeader, apiKey)
	}
}

func toWebSocketURL(baseURL string) string {
	wsURL := strings.TrimSpace(baseURL)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)

	return wsURL
}

func getIntOption(service *config.ServiceConfig, key string, fallback int) int {
	raw := strings.TrimSpace(service.GetOptionsString(key, strconv.Itoa(fallback)))
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}

	return value
}

func samplesForDuration(sampleRate int, durationMs int) int {
	if sampleRate <= 0 || durationMs <= 0 {
		return 0
	}

	return sampleRate * durationMs / 1000
}
