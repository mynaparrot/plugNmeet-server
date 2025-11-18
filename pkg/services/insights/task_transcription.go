package insights

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

type TranscriptionTask struct {
	conf   *config.ServiceConfig
	creds  *config.CredentialsConfig // Store credentials
	logger *logrus.Entry
}

func NewTranscriptionTask(conf *config.ServiceConfig, creds *config.CredentialsConfig, logger *logrus.Entry) (insights.Task, error) {
	return &TranscriptionTask{
		conf:   conf,
		creds:  creds,
		logger: logger,
	}, nil
}

// Run implements the insights.Task interface.
func (t *TranscriptionTask) Run(ctx context.Context, audioStream <-chan []byte, roomID, identity string, options []byte) error {
	// Use the factory to create a provider instance, passing the credentials and model.
	provider, err := NewProvider(t.conf.Provider, t.creds, t.conf.Model, t.logger)
	if err != nil {
		return err
	}

	stream, err := provider.CreateTranscription(ctx, roomID, identity, options)
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
