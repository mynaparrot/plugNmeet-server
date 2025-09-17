package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/logging"
	"github.com/mynaparrot/plugnmeet-server/pkg/routers"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/sirupsen/logrus"
)

func main() {
	configFile := flag.String("config", "config.yaml", "Configuration file")
	showVersion := flag.Bool("version", false, "Show version info")
	flag.Parse()

	if *showVersion {
		fmt.Printf("version: %s\n", version.Version)
		return
	}

	startServer(*configFile)
}

func startServer(configFile string) {
	ctx, cancel := context.WithCancel(context.Background())

	appCnf, err := helpers.ReadYamlConfigFile(configFile)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read config file")
	}
	// set this config for global usage
	config.New(appCnf)

	logger, err := logging.NewLogger(&appCnf.LogSettings)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to setup logger")
	}
	appCnf.Logger = logger

	// now prepare our server
	err = helpers.PrepareServer(ctx, appCnf)
	if err != nil {
		logger.WithError(err).Fatalln("Failed to prepare server")
	}

	appFactory, err := factory.NewAppFactory(ctx, appCnf)
	if err != nil {
		logger.WithError(err).Fatalln("Failed to create app factory")
	}
	// boot up some services
	appFactory.Boot()

	// defer close connections
	defer helpers.HandleCloseConnections()

	rt := routers.New(appFactory.AppConfig, appFactory.Controllers)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		logger.WithField("signal", sig).Infoln("Exit requested, attempting graceful shutdown...")

		if err := rt.ShutdownWithTimeout(15 * time.Second); err != nil {
			logger.WithError(err).Warn("Graceful shutdown failed, forcing exit.")
		}
		cancel()
	}()

	err = rt.Listen(fmt.Sprintf(":%d", appCnf.Client.Port))
	if err != nil {
		logger.WithError(err).Fatalln("Failed to start server")
	}
}
