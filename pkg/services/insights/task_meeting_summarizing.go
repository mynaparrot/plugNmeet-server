package insightsservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	lkMedia "github.com/livekit/media-sdk"
	"github.com/livekit/media-sdk/mixer"
	"github.com/livekit/media-sdk/rtp"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
	"github.com/sirupsen/logrus"
)

type MeetingSummarizingTask struct {
	ctx     context.Context // The agent's main context
	appConf *config.AppConfig
	service *config.ServiceConfig
	logger  *logrus.Entry

	mixer    *mixer.Mixer
	writer   *media.WAVWriter
	initOnce sync.Once
}

// NewMeetingSummarizingTask now accepts the agent's main context.
func NewMeetingSummarizingTask(ctx context.Context, appConf *config.AppConfig, serviceConfig *config.ServiceConfig, logger *logrus.Entry) (insights.Task, error) {
	return &MeetingSummarizingTask{
		ctx:     ctx,
		appConf: appConf,
		service: serviceConfig,
		logger:  logger.WithField("service-task", "meeting_summarizing"),
	}, nil
}

// RunAudioStream now correctly handles the per-participant context.
func (t *MeetingSummarizingTask) RunAudioStream(participantCtx context.Context, audioStream <-chan lkMedia.PCM16Sample, roomId, userId string, options []byte) error {
	var initErr error
	t.initOnce.Do(func() {
		storagePath, ok := t.service.Options["storage_path"].(string)
		if !ok {
			initErr = errors.New("storage_path not configured for meeting_summarizing service")
			t.logger.Error(initErr)
			return
		}

		outputDir := filepath.Join(storagePath, roomId)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			initErr = fmt.Errorf("failed to create output directory: %w", err)
			t.logger.Error(initErr)
			return
		}

		timestamp := time.Now().Unix()
		outputFile := filepath.Join(outputDir, fmt.Sprintf("mixed_audio_%d.wav", timestamp))

		file, err := os.Create(outputFile)
		if err != nil {
			initErr = fmt.Errorf("failed to create output file: %w", err)
			t.logger.Error(initErr)
			return
		}

		writer, err := media.NewWAVWriter(file, 16000, 1, func() {
			t.doRoomSummarizing(roomId, outputFile, options)
		})
		if err != nil {
			initErr = fmt.Errorf("failed to create WAV file writer: %w", err)
			t.logger.Error(initErr)
			return
		}
		t.writer = writer

		newMixer, err := mixer.NewMixer(writer, rtp.DefFrameDur, nil, 1, mixer.DefaultInputBufferFrames)
		if err != nil {
			initErr = fmt.Errorf("failed to create newMixer: %w", err)
			t.logger.Error(initErr)
			return
		}

		t.mixer = newMixer
		t.logger.Infof("newMixer initialized, writing to %s", outputFile)

		// This goroutine listens for the AGENT's shutdown signal.
		go func() {
			<-t.ctx.Done() // Use the main agent context here.
			if t.mixer != nil {
				t.logger.Infoln("stopping mixer")
				t.mixer.Stop()
				err := t.writer.Close()
				if err != nil {
					t.logger.WithError(err).Error("failed to close writer")
				}
			}
		}()
	})

	if initErr != nil {
		return initErr
	}
	if t.mixer == nil {
		return errors.New("mixer not initialized")
	}

	input := t.mixer.NewInput()
	if input == nil {
		return errors.New("failed to create mixer input")
	}
	defer t.mixer.RemoveInput(input)

	log := t.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
	})
	log.Info("starting audio stream for user")

	for {
		select {
		case <-participantCtx.Done(): // per-participant context here.
			log.Info("stopping audio stream for user")
			return nil
		case pcmSample, ok := <-audioStream:
			if !ok {
				return nil
			}
			if err := input.WriteSample(pcmSample); err != nil {
				log.WithError(err).Error("failed to write sample to mixer")
			}
		}
	}
}

// doRoomSummarizing will be called when the file is closed.
// It publishes a job to the NATS queue for a worker to process.
func (t *MeetingSummarizingTask) doRoomSummarizing(roomId, filePath string, options []byte) {
	t.logger.Infof("file writing finished for %s. publishing summarization job.", filePath)

	payload := insights.SummarizeJobPayload{
		RoomId:   roomId,
		FilePath: filePath,
		Options:  options,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.logger.WithError(err).Error("failed to marshal summarization job payload")
		return
	}

	// Publish to the NATS queue.
	err = t.appConf.NatsConn.Publish(insights.SummarizeJobQueue, payloadBytes)
	if err != nil {
		t.logger.WithError(err).Error("failed to publish summarization job")
	}
}

// RunStateless is not implemented for this task.
func (t *MeetingSummarizingTask) RunStateless(ctx context.Context, options []byte) (interface{}, error) {
	return nil, errors.New("RunStateless is not supported for MeetingSummarizingTask")
}
