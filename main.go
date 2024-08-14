package main

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/schedulercontroller"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/websocketcontroller"
	"github.com/mynaparrot/plugnmeet-server/pkg/routers"
	"github.com/mynaparrot/plugnmeet-server/version"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cli.VersionPrinter = func(c *cli.Context) {
		fmt.Printf("%s\n", c.App.Version)
	}

	app := &cli.App{
		Name:        "plugnmeet-server",
		Usage:       "Scalable, Open source web conference system",
		Description: "without option will start server",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "config",
				Usage:       "Configuration file",
				DefaultText: "config.yaml",
				Value:       "config.yaml",
			},
		},
		Action:  startServer,
		Version: version.Version,
	}
	err := app.Run(os.Args)
	if err != nil {
		log.Fatalln(err)
	}
}

func startServer(c *cli.Context) error {
	appCnf, err := helpers.ReadYamlConfigFile(c.String("config"))
	if err != nil {
		panic(err)
	}
	// set this config for global usage
	config.New(appCnf)

	// now prepare our server
	err = helpers.PrepareServer(config.GetConfig())
	if err != nil {
		log.Fatalln(err)
	}

	// we'll subscribe to redis channels now
	go websocketcontroller.SubscribeToWebsocketChannel()
	go schedulercontroller.StartScheduler()

	// defer close connections
	defer helpers.HandleCloseConnections()

	rt := routers.New()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		log.Infoln("exit requested, shutting down", "signal", sig)
		_ = rt.Shutdown()
	}()

	return rt.Listen(fmt.Sprintf(":%d", config.GetConfig().Client.Port))
}
