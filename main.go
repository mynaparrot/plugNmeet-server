package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/mynaparrot/plugnmeet-server/pkg/app"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

func main() {
	configFile := flag.String("config", "config.yaml", "Configuration file")
	showVersion := flag.Bool("version", false, "Show version info")
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s\n", version.Version)
		return
	}

	// Read config early to determine if fx.NopLogger should be used
	isDebug, err := getClientDebugStatus(*configFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read client debug status from config")
	}

	fxOpts := []fx.Option{
		fx.Provide(func(lc fx.Lifecycle) context.Context {
			ctx, cancel := context.WithCancel(context.Background())

			lc.Append(fx.Hook{
				OnStop: func(_ context.Context) error {
					logrus.Info("Shutting down application...")
					cancel()
					return nil
				},
			})
			return ctx
		}),

		fx.Supply(*configFile),
		app.ApplicationModule,
	}

	if !isDebug {
		fxOpts = append(fxOpts, fx.NopLogger)
	}

	a := fx.New(fxOpts...)
	a.Run() // run the app

	if err := a.Err(); err != nil {
		logrus.WithError(err).Fatal("Application failed to run")
	}
}

// getClientDebugStatus reads the config file to determine the Client.Debug status.
// This is a temporary read, the full config will be provided by fx.
func getClientDebugStatus(file string) (bool, error) {
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return false, err
	}

	var tempConfig struct {
		Client config.ClientInfo `yaml:"client"`
	}
	if err := yaml.Unmarshal(yamlFile, &tempConfig); err != nil {
		return false, err
	}

	return tempConfig.Client.Debug, nil
}
