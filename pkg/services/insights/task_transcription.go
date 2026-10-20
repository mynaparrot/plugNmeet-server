package insightsservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

type TranscriptionTask struct {
	appConf      *config.AppConfig
	natsConn     *nats.Conn
	service      *config.ServiceConfig
	account      *config.ProviderAccount
	natsService  *natsservice.NatsService
	redisService *redisservice.RedisService
	logger       *logrus.Entry
}

func NewTranscriptionTask(appConf *config.AppConfig, natsConn *nats.Conn, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, natsService *natsservice.NatsService, redisService *redisservice.RedisService, logger *logrus.Entry) (insights.Task, error) {
	return &TranscriptionTask{
		appConf:      appConf,
		natsConn:     natsConn,
		service:      serviceConfig,
		account:      providerAccount,
		natsService:  natsService,
		redisService: redisService,
		logger:       logger.WithField("service-task", "transcription"),
	}, nil
}

// RunAudioStream implements the insights.Task interface; wake may be nil.
func (t *TranscriptionTask) RunAudioStream(ctx context.Context, audioStream <-chan media.PCM16Sample, wake <-chan struct{}, roomTableId uint64, roomId, userId string, options []byte) error {
	log := t.logger.WithFields(logrus.Fields{
		"method":      "RunAudioStream",
		"roomId":      roomId,
		"roomTableId": roomTableId,
		"userId":      userId,
	})

	args := &ProviderArgs{
		Ctx:             ctx,
		ProviderType:    t.service.Provider,
		ProviderAccount: t.account,
		ServiceConfig:   t.service,
		RDS:             t.redisService.GetRedisClient(),
		Logger:          t.logger,
	}
	provider, err := NewProvider(args)
	if err != nil {
		// Bad credentials/config cannot be retried; report it as terminal.
		t.notifyRoom(roomId, userId, "speech-services.service-stopped", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_ERROR, log)
		return fmt.Errorf("%w: could not create provider: %w", insights.ErrTranscriptionUnrecoverable, err)
	}

	synthesisChannel := fmt.Sprintf(insights.SynthesisNatsChannel, roomId)

	run := newTranscriptionRun(log, audioStream, wake, func(ctx context.Context) (insights.TranscriptionStream, error) {
		return provider.CreateTranscription(ctx, roomId, userId, options)
	})
	run.handleEvent = func(event *insights.TranscriptionEvent) {
		t.handleTranscriptionEvent(event, roomId, userId, synthesisChannel, log)
	}
	run.setUsage = func(active bool) {
		if _, err := t.redisService.HandleTranscriptionUsage(roomId, userId, active); err != nil {
			log.WithError(err).Errorln("update user usage failed")
		}
	}
	run.notifyReady = func() {
		t.notifyRoom(roomId, userId, "speech-services.speech-to-text-ready", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, log)
	}
	run.notifyStopped = func() {
		t.notifyRoom(roomId, userId, "speech-services.service-stopped", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, log)
	}
	run.notifyFailed = func() {
		t.notifyRoom(roomId, userId, "speech-services.service-stopped", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_ERROR, log)
	}

	return run.run(ctx)
}

func (t *TranscriptionTask) handleTranscriptionEvent(event *insights.TranscriptionEvent, roomId, userId, synthesisChannel string, log *logrus.Entry) {
	switch event.Type {
	case insights.EventTypePartialResult, insights.EventTypeFinalResult:
		marshal, err := protojson.Marshal(event.Result)
		if err != nil {
			log.WithError(err).Error("failed to marshal transcription result")
			return
		}
		// Use the real-time pub/sub publisher for high-performance, loss-tolerant events.
		if err = t.natsService.BroadcastSystemPubSubEventToRoom(plugnmeet.NatsMsgServerToClientEvents_TRANSCRIPTION_OUTPUT_TEXT, roomId, marshal); err != nil {
			log.WithError(err).Errorln("error publishing real-time transcription result")
		}

		// If we have a final result, publish it to the dedicated synthesis SynthesisNatsChannel.
		if event.Type == insights.EventTypeFinalResult {
			if err = t.natsConn.Publish(synthesisChannel, marshal); err != nil {
				log.WithError(err).Errorln("error publishing to synthesis SynthesisNatsChannel")
			}
			if event.Result.AllowedTranscriptionStorage {
				if err = t.redisService.AddTranscriptionToHistory(roomId, userId, event.Result.FromUserName, event.Result.Lang, event.Result.Text); err != nil {
					log.WithError(err).Errorln("error adding transcription chunk")
				}
			}
		}
	}
}

func (t *TranscriptionTask) notifyRoom(roomId, userId, message string, level plugnmeet.NatsSystemNotificationTypes, log *logrus.Entry) {
	if err := t.natsService.BroadcastSystemNotificationToRoom(roomId, message, level, false, &userId); err != nil {
		log.WithError(err).Errorln("error broadcasting system notification")
	}
}

// RunStateless is not implemented for TranslationTask as it's a stateless service.
func (t *TranscriptionTask) RunStateless(ctx context.Context, options []byte) (interface{}, error) {
	return nil, errors.New("run is not supported for a stateless transcription task")
}
