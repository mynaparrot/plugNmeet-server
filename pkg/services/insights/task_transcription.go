package insightsservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

type TranscriptionTask struct {
	appConf      *config.AppConfig
	service      *config.ServiceConfig
	account      *config.ProviderAccount
	natsService  *natsservice.NatsService
	redisService *redisservice.RedisService
	logger       *logrus.Entry
}

func NewTranscriptionTask(appConf *config.AppConfig, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, natsService *natsservice.NatsService, redisService *redisservice.RedisService, logger *logrus.Entry) (insights.Task, error) {
	return &TranscriptionTask{
		appConf:      appConf,
		service:      serviceConfig,
		account:      providerAccount,
		natsService:  natsService,
		redisService: redisService,
		logger:       logger.WithField("service-task", "transcription"),
	}, nil
}

// RunAudioStream implements the insights.Task interface.
func (t *TranscriptionTask) RunAudioStream(ctx context.Context, audioStream <-chan media.PCM16Sample, roomTableId uint64, roomId, userId string, options []byte) error {
	// Use the factory to create a provider instance.
	provider, err := NewProvider(ctx, t.service.Provider, t.account, t.service, t.logger)
	if err != nil {
		return err
	}

	stream, err := provider.CreateTranscription(ctx, roomId, userId, options)
	if err != nil {
		return err
	}

	// Goroutine to pipe audio to the provider
	go func() {
		defer stream.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case pcmSample, ok := <-audioStream:
				if !ok {
					return
				}
				if err := stream.WriteSample(pcmSample); err != nil {
					t.logger.WithError(err).Error("error writing audio to provider")
					return
				}
			}
		}
	}()

	// Goroutine to process results
	go func() {
		defer func() {
			if _, err := t.redisService.HandleTranscriptionUsage(roomId, userId, false); err != nil {
				t.logger.WithError(err).Errorln("update user usage failed")
			}

			if err := t.natsService.BroadcastSystemNotificationToRoom(roomId, "speech-services.service-stopped", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, false, &userId); err != nil {
				t.logger.WithError(err).Errorln("error broadcasting system notification")
			}
		}()

		synthesisChannel := fmt.Sprintf(SynthesisChannel, roomId)

		// The loop will automatically break when the SynthesisChannel is closed.
		for event := range stream.Results() {
			switch event.Type {
			case insights.EventTypePartialResult, insights.EventTypeFinalResult:
				marshal, err := protojson.Marshal(event.Result)
				if err != nil {
					return
				}
				if err = t.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_TRANSCRIPTION_OUTPUT_TEXT, roomId, marshal, nil); err != nil {
					t.logger.WithError(err).Errorln("error broadcasting transcription result")
				}

				// If we have a final result, publish it to the dedicated synthesis SynthesisChannel.
				if event.Type == insights.EventTypeFinalResult {
					if err = t.appConf.NatsConn.Publish(synthesisChannel, marshal); err != nil {
						t.logger.WithError(err).Errorln("error publishing to synthesis SynthesisChannel")
					}
					if event.Result.AllowedTranscriptionStorage {
						if err = t.natsService.AddTranscriptionChunk(roomId, userId, event.Result.FromUserName, event.Result.Lang, event.Result.Text); err != nil {
							t.logger.WithError(err).Errorln("error adding transcription chunk")
						}
					}
				}

			case insights.EventTypeSessionStarted:
				if _, err := t.redisService.HandleTranscriptionUsage(roomId, userId, true); err != nil {
					t.logger.WithError(err).Errorln("update user usage failed")
				}

				time.AfterFunc(time.Second*3, func() {
					if err := t.natsService.BroadcastSystemNotificationToRoom(roomId, "speech-services.speech-to-text-ready", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, false, &userId); err != nil {
						t.logger.WithError(err).Errorln("error broadcasting system notification")
					}
				})

			case insights.EventTypeSessionStopped:
				t.logger.Infoln("transcription session stopped")
			case insights.EventTypeError:
				t.logger.Errorln("insights provider error: ", event.Error)
			}
		}
	}()

	return nil
}

// RunStateless is not implemented for TranslationTask as it's a stateless service.
func (t *TranscriptionTask) RunStateless(ctx context.Context, options []byte) (interface{}, error) {
	return nil, errors.New("run is not supported for a stateless translation task")
}
