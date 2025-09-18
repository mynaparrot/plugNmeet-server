package logging

import (
	"errors"
	"strings"

	"github.com/DeRuina/timberjack"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"

	"io"
	"os"
	"runtime"

	"github.com/sirupsen/logrus"
)

// NewLogger creates and configures a new logrus.Logger based on the provided configuration.
func NewLogger(cfg *config.LogSettings) (*logrus.Logger, error) {
	logger := logrus.New()

	// 1. Set Log Level
	logLevel := logrus.InfoLevel
	if cfg.LogLevel != nil && *cfg.LogLevel != "" {
		if lv, err := logrus.ParseLevel(strings.ToLower(*cfg.LogLevel)); err == nil {
			logLevel = lv
		}
	}
	logger.SetLevel(logLevel)

	// 2. Setup Output
	// By default, log to standard output.
	var output io.Writer = os.Stdout

	// If file logging is enabled, create a multi-writer to log to both stdout and the file.
	if cfg.LogFile != "" {
		if cfg.LogFile == "" {
			return nil, errors.New("file logging is enabled but no filepath is provided")
		}

		fileLogger := &timberjack.Logger{
			Filename:   cfg.LogFile,
			MaxSize:    cfg.MaxSize,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
		}
		// Write to both stdout and the file.
		output = io.MultiWriter(os.Stdout, fileLogger)
		// Use a temporary logger to announce this, as the main logger isn't fully configured yet.
		logrus.New().Infof("File logging enabled, writing to %s", cfg.LogFile)
	}
	logger.SetOutput(output)

	// 3. Set Formatter
	textFormatter := &logrus.TextFormatter{
		FullTimestamp: true,
		// Disable the default caller prettyfier to let our custom one take over.
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			return "", ""
		},
		ForceColors: true,
	}

	// 4. Wrap with our custom source formatter
	logger.SetFormatter(&SourceFormatter{
		Underlying: textFormatter,
		AddSpace:   true,
	})

	// 5. Set Caller Reporting
	logger.SetReportCaller(true)

	return logger, nil
}
