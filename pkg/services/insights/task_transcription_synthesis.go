package insightsservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	lkLogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	inMedia "github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// TranscriptionSynthesisTask listens for translation results and dispatches them to language-specific workers for synthesis.
type TranscriptionSynthesisTask struct {
	ctx           context.Context
	cancel        context.CancelFunc
	appCnf        *config.AppConfig
	logger        *logrus.Entry
	provider      insights.Provider
	roomId        string
	e2eeKey       *string
	natsService   *natsservice.NatsService
	redisService  *redisservice.RedisService
	transLangs    []string
	voiceMappings map[string]string

	lock    sync.RWMutex
	workers map[string]*ttsWorker // map[language] -> ttsWorker
	sf      singleflight.Group
}

func NewTranscriptionSynthesisTask(ctx context.Context, appCnf *config.AppConfig, logger *logrus.Entry, provider insights.Provider, serviceConfig *config.ServiceConfig, redisService *redisservice.RedisService, natsService *natsservice.NatsService, roomId string, transLangs []string, e2eeKey *string) *TranscriptionSynthesisTask {
	ctx, cancel := context.WithCancel(ctx)

	return &TranscriptionSynthesisTask{
		ctx:           ctx,
		cancel:        cancel,
		appCnf:        appCnf,
		provider:      provider,
		roomId:        roomId,
		e2eeKey:       e2eeKey,
		natsService:   natsService,
		redisService:  redisService,
		transLangs:    transLangs,
		voiceMappings: serviceConfig.GetVoiceMappings(),
		workers:       make(map[string]*ttsWorker),
		logger:        logger.WithField("sub-task", "transcription-synthesis"),
	}
}

// Run creates all workers and then subscribes to NATS to start the main orchestration loop.
func (t *TranscriptionSynthesisTask) Run() {
	sub, err := t.appCnf.NatsConn.Subscribe(fmt.Sprintf(insights.SynthesisNatsChannel, t.roomId), func(msg *nats.Msg) {
		res := new(plugnmeet.InsightsTranscriptionResult)
		err := protojson.Unmarshal(msg.Data, res)
		if err != nil {
			t.logger.WithError(err).Error("failed to unmarshal transcription result")
			return
		}

		// We only care about final results with translations
		if res.IsPartial || len(res.Translations) == 0 {
			return
		}

		t.dispatch(res.FromUserId, res.Translations)
	})

	if err != nil {
		t.logger.WithError(err).Error("failed to subscribe to NATS for synthesis task")
		return
	}
	t.logger.Infof("successfully connected with NATS for synthesis channel: '%s'", sub.Subject)

	// Eagerly create all workers for the configured languages now that NATS is connected.
	for _, lang := range t.transLangs {
		_, err := t.createAgentWorker(lang)
		if err != nil {
			t.logger.WithError(err).Errorf("failed to create initial tts worker for language %s", lang)
			// We continue even if one fails, others might succeed.
		}
	}

	// Wait for the context to be canceled, then unsubscribe
	<-t.ctx.Done()
	_ = sub.Unsubscribe()
}

// dispatch sends translated text to the appropriate worker queue.
func (t *TranscriptionSynthesisTask) dispatch(fromUserId string, translations map[string]string) {
	for lang, text := range translations {
		if text == "" {
			continue
		}

		t.lock.RLock()
		worker, ok := t.workers[lang]
		t.lock.RUnlock()

		if !ok {
			// This is a reconciliation step. If a worker wasn't created at startup, create it now.
			t.logger.Warnf("worker for language %s not found, creating it on-the-fly", lang)
			var err error
			worker, err = t.createAgentWorker(lang)
			if err != nil {
				t.logger.WithError(err).Errorf("failed to create reconciled tts worker for language %s", lang)
				continue
			}
		}
		if err := t.redisService.UpdateTTSServiceUsage(t.ctx, t.roomId, fromUserId, lang, len(text)); err != nil {
			t.logger.WithError(err).Error("failed to update TTS service usage")
		}
		// Send text to the worker's queue. This is non-blocking.
		worker.workQueue <- text
	}
}

// createAgentWorker creates a new worker using singleflight to prevent duplicate creation.
func (t *TranscriptionSynthesisTask) createAgentWorker(language string) (*ttsWorker, error) {
	// Fast path: Check if the worker already exists with a read lock.
	t.lock.RLock()
	if w, ok := t.workers[language]; ok {
		t.lock.RUnlock()
		return w, nil
	}
	t.lock.RUnlock()

	// Worker doesn't exist. Use singleflight to ensure the creation logic
	// runs only once for this language.
	v, err, _ := t.sf.Do(language, func() (interface{}, error) {
		// This function is now guaranteed to run only once for a given language
		// across all concurrent goroutines.

		// All expensive I/O happens here, inside the singleflight guard.
		voice := t.voiceMappings[language]
		agentIdentity := fmt.Sprintf("%s%s", config.TTSAgentUserIdPrefix, language)
		agentName := fmt.Sprintf("Translator-%s", strings.ToUpper(language))

		log := t.logger.WithFields(logrus.Fields{
			"agent_identity": agentIdentity,
			"agent_name":     agentName,
			"language":       language,
			"voice":          voice,
		})

		workerRoom, err := t.connectAgentToRoom(agentIdentity, agentName, log)
		if err != nil {
			return nil, err
		}

		// wait before publishing
		time.Sleep(time.Second * 2)
		publisher, err := inMedia.NewAudioPublisher(workerRoom, language, 16000, 1, t.e2eeKey)
		if err != nil {
			workerRoom.Disconnect() // Clean up
			return nil, err
		}

		ctx, cancel := context.WithCancel(t.ctx)
		worker := &ttsWorker{
			ctx:       ctx,
			cancel:    cancel,
			provider:  t.provider,
			room:      workerRoom,
			publisher: publisher,
			workQueue: make(chan string, 10),
			language:  language,
			voice:     voice,
			logger:    t.logger.WithField("tts_language", language),
		}

		// Now, safely add the newly created worker to the shared map.
		t.lock.Lock()
		t.workers[language] = worker
		t.lock.Unlock()

		go worker.run(log)
		log.Infof("created new tts agent participant for language %s with voice %s", language, voice)

		return worker, nil
	})

	if err != nil {
		return nil, err
	}

	return v.(*ttsWorker), nil
}

func (t *TranscriptionSynthesisTask) connectAgentToRoom(agentIdentity, agentName string, log *logrus.Entry) (*lksdk.Room, error) {
	// Generate identity and token for the new participant
	claims := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:   t.roomId,
		UserId:   agentIdentity,
		IsHidden: false,
		Name:     agentName,
	}
	token, err := auth.GenerateLivekitAccessToken(t.appCnf.LivekitInfo.ApiKey, t.appCnf.LivekitInfo.Secret, time.Minute*5, claims)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token for tts worker: %w", err)
	}

	// add user to our plugNmeet room manually
	mt := plugnmeet.UserMetadata{
		IsAdmin:         true,
		RecordWebcam:    proto.Bool(false),
		WaitForApproval: false,
		LockSettings: &plugnmeet.LockSettings{
			LockWebcam:     proto.Bool(false),
			LockMicrophone: proto.Bool(false),
		},
	}
	err = t.natsService.AddUser(t.roomId, agentIdentity, agentName, true, false, &mt)
	if err != nil {
		log.WithError(err).Errorln("failed to add ingress user to NATS")
		return nil, err
	}

	// Do proper user status update
	err = t.natsService.UpdateUserStatus(t.roomId, agentIdentity, natsservice.UserStatusOnline)
	if err != nil {
		return nil, err
	}

	userInfo, err := t.natsService.GetUserInfo(t.roomId, agentIdentity)
	if err != nil {
		return nil, err
	}

	err = t.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, t.roomId, userInfo, agentIdentity)
	if err != nil {
		return nil, err
	}
	log.Info("successfully added tts agent & broadcasted status")

	// Create and connect a new room object for the worker
	workerRoom := lksdk.NewRoom(&lksdk.RoomCallback{
		OnDisconnected: func() {
			log.Infoln("tts worker disconnected from room")
		},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnLocalTrackPublished: func(publication *lksdk.LocalTrackPublication, lp *lksdk.LocalParticipant) {
				log.Infof("successfully published track %s to room, Encryption_Type: %s", publication.TrackInfo().Name, publication.TrackInfo().Encryption)
			},
		},
	})

	workerRoom.SetLogger(lkLogger.GetLogger())
	if err = workerRoom.JoinWithToken(t.appCnf.LivekitInfo.Host, token, lksdk.WithAutoSubscribe(false)); err != nil {
		// make user offline
		err = t.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_DISCONNECTED, t.roomId, userInfo, agentIdentity)
		return nil, fmt.Errorf("tts worker failed to join room: %w", err)
	}
	log.Info("tts agent joining completed successfully")

	return workerRoom, nil
}

// Shutdown gracefully stops the TranscriptionSynthesisTask and all its workers.
func (t *TranscriptionSynthesisTask) Shutdown() {
	t.cancel() // This will stop the NATS subscription and all worker contexts

	// Collect workers first to avoid holding lock during disconnect
	t.lock.RLock()
	workersToClose := make([]*ttsWorker, 0, len(t.workers))
	for _, worker := range t.workers {
		workersToClose = append(workersToClose, worker)
	}
	t.lock.RUnlock()

	// Now cancel context
	for _, worker := range workersToClose {
		worker.cancel()
	}

	t.logger.Info("transcription synthesis task shut down")
}

// ttsWorker manages the synthesis queue and a dedicated LiveKit participant for a single language.
type ttsWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	provider  insights.Provider
	room      *lksdk.Room
	publisher *inMedia.AudioPublisher
	workQueue chan string
	language  string
	voice     string
	logger    *logrus.Entry
}

// run is the main loop for a ttsWorker. It processes the queue.
func (w *ttsWorker) run(log *logrus.Entry) {
	defer func() {
		w.publisher.Close()
		w.room.Disconnect()
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		case text := <-w.workQueue:
			opts := &insights.SynthesisTaskOptions{
				Text:     text,
				Language: w.language,
				Voice:    w.voice,
			}
			options, err := json.Marshal(opts)
			if err != nil {
				log.WithError(err).Error("failed to marshal synthesis options")
				continue
			}

			audioStream, err := w.provider.SynthesizeText(w.ctx, options)
			if err != nil {
				log.WithError(err).Error("failed to synthesize text")
				continue
			}

			_, err = io.Copy(w.publisher, audioStream)
			_ = audioStream.Close()

			if err != nil && err != io.EOF {
				log.WithError(err).Error("failed to write audio stream to track")
			}
		}
	}
}
