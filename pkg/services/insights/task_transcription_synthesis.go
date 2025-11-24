package insightsservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	lkLogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const SynthesisChannel = "plug-n-meet-transcription-output-%s"

// audioTrackWriter is an adapter to make *lkmedia.PCMLocalTrack implement io.Writer.
type audioTrackWriter struct {
	track *lkmedia.PCMLocalTrack
}

// Write converts the raw PCM byte slice and writes it to the track.
func (w *audioTrackWriter) Write(data []byte) (n int, err error) {
	// The data from Azure is 16kHz 16-bit mono PCM.
	// We need to convert it to []int16 for the PCMLocalTrack.
	numSamples := len(data) / 2
	if numSamples == 0 {
		return len(data), nil
	}

	samples := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		// Assuming little-endian byte order for 16-bit PCM
		samples[i] = int16(data[i*2]) | int16(data[i*2+1])<<8
	}

	mediaSample := media.PCM16Sample(samples)
	err = w.track.WriteSample(mediaSample)
	if err != nil {
		return 0, err
	}

	return len(data), nil
}

// ttsWorker manages the synthesis queue and a dedicated LiveKit participant for a single language.
type ttsWorker struct {
	ctx       context.Context
	cancel    context.CancelFunc
	provider  insights.Provider
	room      *lksdk.Room // The worker's own room connection
	track     *lkmedia.PCMLocalTrack
	workQueue chan string
	language  string
	voice     string
	logger    *logrus.Entry
}

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
	sub, err := t.appCnf.NatsConn.Subscribe(fmt.Sprintf(SynthesisChannel, t.roomId), func(msg *nats.Msg) {
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
		_, err := t.createWorker(lang)
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
			worker, err = t.createWorker(lang)
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

// createWorker creates a new worker, including a new LiveKit participant, and a processing goroutine.
func (t *TranscriptionSynthesisTask) createWorker(language string) (*ttsWorker, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	// Double-check if another thread created it while we were waiting for the lock
	if w, ok := t.workers[language]; ok {
		return w, nil
	}

	// Get the voice from the mapping. It's okay if it's empty, the provider will use its default.
	voice := t.voiceMappings[language]
	workerIdentity := fmt.Sprintf("%s-%s", config.TTSAgentUserIdPrefix, language)
	workerName := fmt.Sprintf("Translator-%s", strings.ToUpper(language))

	log := t.logger.WithFields(logrus.Fields{
		"userId": workerIdentity,
		"name":   workerName,
	})
	// Create this user to Nats KV first
	mt := plugnmeet.UserMetadata{
		IsAdmin:         true,
		RecordWebcam:    proto.Bool(false),
		WaitForApproval: false,
		LockSettings: &plugnmeet.LockSettings{
			LockWebcam:     proto.Bool(false),
			LockMicrophone: proto.Bool(false),
		},
	}
	err := t.natsService.AddUser(t.roomId, workerIdentity, workerName, true, false, &mt)
	if err != nil {
		log.WithError(err).Errorln("failed to add ingress user to NATS")
		return nil, err
	}
	log.Info("successfully added tts participant to NATS user bucket")

	// Generate identity and token for the new participant
	claims := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:   t.roomId,
		UserId:   workerIdentity,
		IsHidden: false,
		Name:     workerName,
	}
	token, err := auth.GenerateLivekitAccessToken(t.appCnf.LivekitInfo.ApiKey, t.appCnf.LivekitInfo.Secret, time.Minute*5, claims)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token for tts worker: %w", err)
	}

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
		return nil, fmt.Errorf("tts worker failed to join room: %w", err)
	}

	// Do proper user status update
	err = t.natsService.UpdateUserStatus(t.roomId, workerIdentity, natsservice.UserStatusOnline)
	if err != nil {
		return nil, err
	}
	userInfo, err := t.natsService.GetUserInfo(t.roomId, workerIdentity)
	if err != nil {
		return nil, err
	}
	err = t.natsService.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, t.roomId, userInfo, workerIdentity)
	if err != nil {
		return nil, err
	}

	// Create track options, including encryptor if E2EE is enabled
	var trackOpts []lkmedia.PCMLocalTrackOption
	pubOpts := &lksdk.TrackPublicationOptions{
		Name:   language,
		Source: livekit.TrackSource_MICROPHONE,
	}

	if t.e2eeKey != nil && *t.e2eeKey != "" {
		key, err := lksdk.DeriveKeyFromString(*t.e2eeKey)
		if err != nil {
			workerRoom.Disconnect()
			return nil, fmt.Errorf("failed to derive key for tts encryptor: %w", err)
		}
		// Use 0 for the key ID as per the GCM standard
		encryptor, err := lkmedia.NewGCMEncryptor(key, 0)
		if err != nil {
			workerRoom.Disconnect()
			return nil, fmt.Errorf("failed to create tts encryptor: %w", err)
		}
		trackOpts = append(trackOpts, lkmedia.WithEncryptor(encryptor))
		// Set the encryption type on the publication options
		pubOpts.Encryption = livekit.Encryption_GCM
	}

	// Create the local audio track
	track, err := lkmedia.NewPCMLocalTrack(16000, 1, nil, trackOpts...)
	if err != nil {
		workerRoom.Disconnect()
		return nil, fmt.Errorf("failed to create pcm track for tts worker: %w", err)
	}

	// Publish the track
	if _, err = workerRoom.LocalParticipant.PublishTrack(track, pubOpts); err != nil {
		track.Close()
		workerRoom.Disconnect()
		return nil, fmt.Errorf("tts worker failed to publish track: %w", err)
	}

	ctx, cancel := context.WithCancel(t.ctx)
	worker := &ttsWorker{
		ctx:       ctx,
		cancel:    cancel,
		provider:  t.provider,
		room:      workerRoom,
		track:     track,
		workQueue: make(chan string, 10),
		language:  language,
		voice:     voice,
		logger:    t.logger.WithField("tts_language", language),
	}

	go worker.run()
	t.workers[language] = worker
	log.Infof("created new tts worker participant for language %s with voice %s", language, voice)

	return worker, nil
}

// run is the main loop for a ttsWorker. It processes the queue.
func (w *ttsWorker) run() {
	defer w.track.Close()
	defer w.room.Disconnect()
	writer := &audioTrackWriter{track: w.track}

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
				w.logger.WithError(err).Error("failed to marshal synthesis options")
				continue
			}

			audioStream, err := w.provider.SynthesizeText(w.ctx, options)
			if err != nil {
				w.logger.WithError(err).Error("failed to synthesize text")
				continue
			}

			_, err = io.Copy(writer, audioStream)
			_ = audioStream.Close()

			if err != nil && err != io.EOF {
				w.logger.WithError(err).Error("failed to write audio stream to track")
			}
		}
	}
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

	// Now disconnect them without holding the lock
	for _, worker := range workersToClose {
		worker.room.Disconnect()
	}

	t.logger.Info("transcription synthesis task shut down")
}
