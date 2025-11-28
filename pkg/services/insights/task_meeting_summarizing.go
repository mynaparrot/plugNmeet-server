package insightsservice

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/livekit/media-sdk/mixer"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
	"github.com/sirupsen/logrus"
)

type MeetingSummarizingTask struct {
	ctx     context.Context
	appConf *config.AppConfig
	service *config.ServiceConfig
	logger  *logrus.Entry

	mixer    *mixer.Mixer
	writer   *media.PCMFileWriter
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
func (t *MeetingSummarizingTask) RunAudioStream(participantCtx context.Context, audioStream <-chan []byte, roomId, userId string, options []byte) error {
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
		outputFile := filepath.Join(outputDir, fmt.Sprintf("mixed_audio_%d.pcm", timestamp))

		writer, err := media.NewPCMFileWriter(outputFile, media.WithOnCloseCallback(func() {
			t.doRoomSummarizing(roomId, outputFile)
		}))
		if err != nil {
			initErr = fmt.Errorf("failed to create PCM file writer: %w", err)
			t.logger.Error(initErr)
			return
		}
		t.writer = writer

		const sampleRate = 48000
		const bufferDuration = 20 * time.Millisecond
		newMixer, err := mixer.NewMixer(writer, bufferDuration, nil, 1, sampleRate)
		if err != nil {
			initErr = fmt.Errorf("failed to create newMixer: %w", err)
			t.logger.Error(initErr)
			return
		}

		t.mixer = newMixer
		t.logger.Infof("newMixer initialized, writing to %s", outputFile)

		// This goroutine listens for the AGENT's shutdown signal.
		go func() {
			<-t.ctx.Done()
			if t.mixer != nil {
				t.mixer.Stop()
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
		case <-participantCtx.Done():
			log.Info("stopping audio stream for user")
			return nil
		case pcmBytes, ok := <-audioStream:
			if !ok {
				return nil
			}
			pcmSample := make([]int16, len(pcmBytes)/2)
			for i := 0; i < len(pcmSample); i++ {
				pcmSample[i] = int16(binary.LittleEndian.Uint16(pcmBytes[i*2:]))
			}

			if err := input.WriteSample(pcmSample); err != nil {
				log.WithError(err).Error("failed to write sample to mixer")
			}
		}
	}
}

// doRoomSummarizing will be called when the file is closed.
func (t *MeetingSummarizingTask) doRoomSummarizing(roomId, filePath string) {
	t.logger.Infof("file writing finished for %s with file %s. starting post-processing.", roomId, filePath)
	// Post-processing logic will go here in the future.
}

// RunStateless is not implemented for this task.
func (t *MeetingSummarizingTask) RunStateless(ctx context.Context, options []byte) (interface{}, error) {
	return nil, errors.New("RunStateless is not supported for MeetingSummarizingTask")
}
