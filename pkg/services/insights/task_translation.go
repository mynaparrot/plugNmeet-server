package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// TranslationTask implements the insights.Task interface for stateless text translation.
type TranslationTask struct {
	conf   *config.ServiceConfig
	creds  *config.CredentialsConfig
	logger *logrus.Entry
}

func NewTranslationTask(conf *config.ServiceConfig, creds *config.CredentialsConfig, logger *logrus.Entry) (insights.Task, error) {
	return &TranslationTask{
		conf:   conf,
		creds:  creds,
		logger: logger,
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
	provider, err := NewProvider(t.conf.Provider, t.creds, t.conf.Model, t.logger)
	if err != nil {
		return nil, err
	}

	// Call the provider's TranslateText method and return the channel.
	return provider.TranslateText(ctx, opts.Text, opts.SourceLang, opts.TargetLangs)
}

// RunAudioStream is not implemented for TranslationTask as it's a stateless service.
func (t *TranslationTask) RunAudioStream(ctx context.Context, audioStream <-chan []byte, roomName, identity string, options []byte) error {
	return errors.New("run is not supported for a stateless translation task")
}
