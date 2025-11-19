package insightsservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

type TranscriptionTask struct {
	service *config.ServiceConfig
	account *config.ProviderAccount
	logger  *logrus.Entry
}

func NewTranscriptionTask(serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, logger *logrus.Entry) (insights.Task, error) {
	return &TranscriptionTask{
		service: serviceConfig,
		account: providerAccount,
		logger:  logger,
	}, nil
}

// RunAudioStream implements the insights.Task interface.
func (t *TranscriptionTask) RunAudioStream(ctx context.Context, audioStream <-chan []byte, roomID, identity string, options []byte) error {
	// Use the factory to create a provider instance.
	provider, err := NewProvider(t.service.Provider, t.account, t.service, t.logger)
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
				fmt.Println(fmt.Sprintf("%+v", result))
			}
		}
	}()

	return nil
}

// RunStateless is not implemented for TranslationTask as it's a stateless service.
func (t *TranscriptionTask) RunStateless(ctx context.Context, options []byte) error {
	return errors.New("run is not supported for a stateless translation task")
}
