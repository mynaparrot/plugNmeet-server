package insights

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// TranscriptionTask handles the entire pipeline for a transcription service.
type TranscriptionTask struct {
	conf   *config.ServiceConfig
	logger *logrus.Entry
}

func NewTranscriptionTask(conf *config.ServiceConfig, logger *logrus.Entry) (*TranscriptionTask, error) {
	return &TranscriptionTask{
		conf:   conf,
		logger: logger,
	}, nil
}

// Run implements the insights.Task interface.
func (t *TranscriptionTask) Run(ctx context.Context, audioStream <-chan []byte, roomID, identity string, options []byte) error {
	// Note: We use the factory from the parent 'insights' package.
	provider := NewProvider(t.conf.Provider, t.conf, t.logger)

	stream, err := provider.CreateTranscription(ctx, roomID, identity, "en", options)
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
			case audioChunk, ok := <-audioStream:
				if !ok {
					return
				}
				if _, err := stream.Write(audioChunk); err != nil {
					t.logger.WithError(err).Error("error writing audio to provider")
					return
				}
			}
		}
	}()

	// Goroutine to process results
	go func() {
		resultsChan := stream.Results()
		for {
			select {
			case <-ctx.Done():
				return
			case result, ok := <-resultsChan:
				if !ok {
					return
				}
				// TODO: Publish to NATS
				t.logger.Infof("Result for %s: (Partial: %v) %s", identity, result.IsPartial, result.Text)
			}
		}
	}()

	return nil
}
