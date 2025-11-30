package insightsservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// TranslationTask implements the insights.Task interface for stateless text translation.
type TranslationTask struct {
	service *config.ServiceConfig
	account *config.ProviderAccount
	logger  *logrus.Entry
}

func NewTranslationTask(serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, logger *logrus.Entry) (insights.Task, error) {
	return &TranslationTask{
		service: serviceConfig,
		account: providerAccount,
		logger:  logger,
	}, nil
}

// RunStateless implements the insights.Task interface for stateless execution.
func (t *TranslationTask) RunStateless(ctx context.Context, options []byte) (interface{}, error) {
	var opts insights.TranslationTaskOptions
	if err := json.Unmarshal(options, &opts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal translation options: %w", err)
	}

	if opts.Text == "" || len(opts.TargetLangs) == 0 {
		return nil, errors.New("text and at least one target_lang are required for translation")
	}

	// Use the factory to create a provider instance.
	provider, err := NewProvider(ctx, t.service.Provider, t.account, t.service, t.logger)
	if err != nil {
		return nil, err
	}

	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Call the provider's synchronous TranslateText method
	result, err := provider.TranslateText(opCtx, opts.Text, opts.SourceLang, opts.TargetLangs)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// RunAudioStream is not implemented for TranslationTask as it's a stateless service.
func (t *TranslationTask) RunAudioStream(ctx context.Context, audioStream <-chan media.PCM16Sample, roomName, identity string, options []byte) error {
	return errors.New("RunAudioStream is not supported for a translation task")
}
