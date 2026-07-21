package insightsservice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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

const maxConsecutiveErrors = 30

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

// RunAudioStream implements the insights.Task interface.
func (t *TranscriptionTask) RunAudioStream(ctx context.Context, audioStream <-chan media.PCM16Sample, roomTableId uint64, roomId, userId string, options []byte) error {
	// Use the factory to create a provider instance.
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
		return err
	}
	log := t.logger.WithFields(logrus.Fields{
		"method":      "RunAudioStream",
		"roomId":      roomId,
		"roomTableId": roomTableId,
		"userId":      userId,
	})

	stream, err := provider.CreateTranscription(ctx, roomId, userId, options)
	if err != nil {
		return err
	}

	// Derive a task-local context so either goroutine can signal the other to
	// stop without affecting the caller's context.
	taskCtx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine to pipe audio to the provider.
	go func() {
		defer wg.Done()
		defer cancel()       // notify the results goroutine that audio is done
		defer stream.Close() // close the stream (closes the results channel)

		// Tolerate transient WriteSample failures (e.g. brief provider hiccups)
		// but bail when the provider is clearly broken for a sustained period.
		var consecutiveErrors int

		for {
			select {
			case <-taskCtx.Done():
				return
			case pcmSample, ok := <-audioStream:
				if !ok {
					log.Infoln("audio stream closed")
					return
				}
				if err := stream.WriteSample(pcmSample); err != nil {
					consecutiveErrors++
					// A nil ctx error here means the stream is done for good.
					if errors.Is(err, context.Canceled) || consecutiveErrors >= maxConsecutiveErrors {
						log.WithError(err).Errorf("stopping audio pipe after %d consecutive write failures", consecutiveErrors)
						return
					}
					log.WithError(err).Warnf("transient audio write failure (%d/%d)", consecutiveErrors, maxConsecutiveErrors)
					continue
				}
				consecutiveErrors = 0
			}
		}
	}()

	// Goroutine to process results.
	go func() {
		defer wg.Done()
		defer cancel() // notify the audio goroutine that results are done
		defer func() {
			if _, err := t.redisService.HandleTranscriptionUsage(roomId, userId, false); err != nil {
				log.WithError(err).Errorln("update user usage failed")
			}

			if err := t.natsService.BroadcastSystemNotificationToRoom(roomId, "speech-services.service-stopped", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, false, &userId); err != nil {
				log.WithError(err).Errorln("error broadcasting system notification")
			}
		}()

		synthesisChannel := fmt.Sprintf(insights.SynthesisNatsChannel, roomId)

		// The loop breaks when the stream is closed or the task context is cancelled.
		resultsCh := stream.Results()
		for {
			select {
			case <-taskCtx.Done():
				// Drain any buffered results that the provider emitted before
				// Close() takes effect; exit when the channel closes.
				for event := range resultsCh {
					t.handleTranscriptionEvent(event, roomId, userId, synthesisChannel, log)
				}
				return
			case event, ok := <-resultsCh:
				if !ok {
					return
				}
				t.handleTranscriptionEvent(event, roomId, userId, synthesisChannel, log)
			}
		}
	}()

	// Block until both goroutines finish so the caller (e.g. room_agent) can
	// properly wait for the full task lifecycle via its own WaitGroup.
	wg.Wait()
	return nil
}

// handleTranscriptionEvent processes a single transcription event.
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

	case insights.EventTypeSessionStarted:
		if _, err := t.redisService.HandleTranscriptionUsage(roomId, userId, true); err != nil {
			log.WithError(err).Errorln("update user usage failed")
		}

		time.AfterFunc(time.Second*5, func() {
			if err := t.natsService.BroadcastSystemNotificationToRoom(roomId, "speech-services.speech-to-text-ready", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, false, &userId); err != nil {
				log.WithError(err).Errorln("error broadcasting system notification")
			}
		})

	case insights.EventTypeSessionStopped:
		log.Infoln("transcription session stopped")
	case insights.EventTypeError:
		log.Errorln("insights provider error: ", event.Error)
	}
}

// RunStateless is not implemented for TranslationTask as it's a stateless service.
func (t *TranscriptionTask) RunStateless(ctx context.Context, options []byte) (interface{}, error) {
	return nil, errors.New("run is not supported for a stateless transcription task")
}
