package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/logging"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/routers"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/sirupsen/logrus"
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
	// 1. Create a context that can be canceled to signal all services to shut down.
	ctx, cancel := context.WithCancel(context.Background())

	// 2. Read the main configuration from the YAML file.
	appCnf, err := helpers.ReadYamlConfigFile(configFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read config file")
	}

	// 3. Initialize the configuration, setting default values and creating necessary directories.
	appCnf, err = config.New(appCnf)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize config")
	}

	// 4. Set up the structured logger (logrus) based on the configuration.
	logger, err := logging.NewLogger(&appCnf.LogSettings)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to setup logger")
	}
	appCnf.Logger = logger

	// 5. Prepare server dependencies like database, Redis, and NATS connections.
	err = helpers.PrepareServer(ctx, appCnf)
	if err != nil {
		logger.WithError(err).Fatalln("Failed to prepare server")
	}

	// 6. Use the dependency injection container (wire) to build the main application object,
	//    which includes all the controllers.
	appFactory, err := factory.NewAppFactory(ctx, appCnf)
	if err != nil {
		logger.WithError(err).Fatalln("Failed to create app factory")
	}
	// 7. Boot up background services (e.g., NATS listeners, janitor for cleanup tasks).
	appFactory.Boot()

	// 8. Defer the closing of connections (DB, Redis, NATS) to ensure they are closed gracefully on exit.
	defer helpers.HandleCloseConnections(appFactory.AppConfig)

	// 9. Create a new Fiber router and register all the application routes.
	rt := routers.New(appFactory.AppConfig, appFactory.Controllers)

	// 10. Set up a channel to listen for OS signals (like Ctrl+C) for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	// 11. Start a goroutine to handle the shutdown process when a signal is received.
	go func() {
		sig := <-sigChan
		logger.WithField("signal", sig).Infoln("Exit requested, attempting graceful shutdown...")

		// Attempt to gracefully shut down the Fiber server, waiting for active connections to finish.
		if err := rt.ShutdownWithTimeout(15 * time.Second); err != nil {
			logger.WithError(err).Warn("Graceful shutdown failed, forcing exit.")
		}
		// Cancel the context to signal all other parts of the application (like background services) to stop.
		cancel()
	}()

	// 12. Start the Fiber web server and listen for incoming HTTP requests. This is a blocking call.
	err = rt.Listen(fmt.Sprintf(":%d", appFactory.AppConfig.Client.Port))
	if err != nil {
		logger.WithError(err).Fatalln("Failed to start server")
	}
}
