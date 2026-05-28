package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	lkLogger "github.com/livekit/protocol/logger"
	"github.com/mynaparrot/plugnmeet-protocol/logging"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/routers"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/sirupsen/logrus"
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

	startServer(*configFile)
}

func startServer(configFile string) {
	// Create a context that can be canceled to signal all services to shut down.
	ctx, cancel := context.WithCancel(context.Background())

	// Read the main configuration from the YAML file.
	cnf, err := readYamlConfigFile(configFile)
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

	// Prepare server dependencies like database, Redis, and NATS connections.
	appCnf, err = factory.InitConnections(ctx, appCnf)
	if err != nil {
		logger.WithError(err).Fatal("Failed to prepare server")
	}

	// Use the dependency injection container (wire) to build the main application object,
	//    which includes all the controllers.
	appFactory, err := factory.NewAppFactory(ctx, appCnf)
	if err != nil {
		logger.WithError(err).Fatal("Failed to create app factory")
	}
	// Boot up background services (e.g., NATS listeners, janitor for cleanup tasks).
	appFactory.Boot()

	// Create a new Fiber router and register all the application routes.
	rt := routers.New(appFactory.AppConfig, appFactory.Controllers)

	// Set up a channel to listen for OS signals (like Ctrl+C) for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// Start a goroutine to handle the shutdown process when a signal is received.
	go func() {
		sig := <-sigChan
		logger.WithField("signal", sig).Info("Exit requested, attempting graceful shutdown...")

		// shut down the application
		appFactory.Shutdown()

		// Attempt to gracefully shut down the Fiber server, waiting for active connections to finish.
		if err := rt.ShutdownWithTimeout(15 * time.Second); err != nil {
			logger.WithError(err).Warn("Graceful shutdown failed, forcing exit.")
		}
		// Cancel the context to signal all other parts of the application (like background services) to stop.
		cancel()
	}()

	appCnf.Logger.WithFields(logrus.Fields{
		"version": version.Version,
		"port":    appFactory.AppConfig.Client.Port,
	}).Info("Starting plugNmeet server")

	// Start the Fiber web server and listen for incoming HTTP requests. This is a blocking call.
	if err := rt.Listen(fmt.Sprintf(":%d", appFactory.AppConfig.Client.Port)); err != nil {
		logger.WithError(err).Fatal("Failed to start server")
	}
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
