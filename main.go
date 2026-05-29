package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	lkLogger "github.com/livekit/protocol/logger"
	"github.com/mynaparrot/plugnmeet-protocol/logging"
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

	// Create a context that can be canceled to signal all services to shut down.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Read the main configuration from the YAML file.
	cnf, err := readYamlConfigFile(*configFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read config file")
	}

	// Initialize the configuration, setting default values and creating necessary directories.
	appCnf, err := config.InitAppConfig(ctx, cnf)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize config")
	}

	// Set up the structured logger (logrus) based on the configuration.
	logger, err := logging.NewLogger(&appCnf.LogSettings)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to setup logger")
	}
	appCnf.Logger = logger
	// to avoid pion logs
	logConf := &lkLogger.Config{
		Level: "warn",
	}
	lkLogger.InitFromConfig(logConf, "pnm")

	fxOpts := []fx.Option{
		fx.Provide(func() context.Context { return ctx }),
		fx.Supply(appCnf, appCnf.Logger),
		app.ApplicationModule,
	}
	if !appCnf.Client.Debug {
		fxOpts = append(fxOpts, fx.NopLogger)
	}

	fx.New(fxOpts...).Run()
}

func readYamlConfigFile(file string) (*config.AppConfig, error) {
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var appCnf config.AppConfig
	if err := yaml.Unmarshal(yamlFile, &appCnf); err != nil {
		return nil, err
	}

	// get current working dir
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// set the root path
	appCnf.RootWorkingDir = wd

	return &appCnf, err
}
