package insightsservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
func (t *TranslationTask) RunStateless(ctx context.Context, options []byte) error {
	var opts insights.TranslationTaskOptions
	if err := json.Unmarshal(options, &opts); err != nil {
		return fmt.Errorf("failed to unmarshal translation options: %w", err)
	}

	if opts.Text == "" || len(opts.TargetLangs) == 0 {
		return errors.New("text and at least one target_lang are required for translation")
	}

	// Use the factory to create a provider instance.
	provider, err := NewProvider(t.service.Provider, t.account, t.service, t.logger)
	if err != nil {
		return err
	}

	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Call the provider's TranslateText method
	resultChan, err := provider.TranslateText(opCtx, opts.Text, opts.SourceLang, opts.TargetLangs)
	if err != nil {
		return err
	}

	go func() {
		select {
		case result, ok := <-resultChan:
			if !ok {
				// This can happen if an error occurred inside the goroutine (e.g., network error).
				// Check your logs for more details.
				fmt.Println("Translation channel closed without a result.")
				return
			}

			// Success! We have the result.
			fmt.Println("\n--- Translation Successful ---")
			fmt.Printf("Original Text: '%s' (from %s)\n", result.Text, result.SourceLang)
			fmt.Println("Translations:")
			for lang, translatedText := range result.Translations {
				fmt.Printf("  - %s: '%s'\n", lang, translatedText)
			}
		}
	}()

	return nil
}

// RunAudioStream is not implemented for TranslationTask as it's a stateless service.
func (t *TranslationTask) RunAudioStream(ctx context.Context, audioStream <-chan []byte, roomName, identity string, options []byte) error {
	return errors.New("RunAudioStream is not supported for a translation task")
}
