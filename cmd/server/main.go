package main

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/handler"
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
	appCnf, err := helpers.ReadConfig(c.String("config"))
	if err != nil {
		panic(err)
	}
	// set this config for global usage
	config.NewAppConfig(appCnf)

	// now prepare our server
	err = helpers.PrepareServer(config.GetConfig())
	if err != nil {
		log.Fatalln(err)
	}

	// we'll subscribe to redis channels now
	go controllers.SubscribeToWebsocketChannel()
	go controllers.StartScheduler()

	// defer close connections
	defer helpers.HandleCloseConnections()

	router := handler.Router()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		log.Infoln("exit requested, shutting down", "signal", sig)
		_ = router.Shutdown()
	}()

	return router.Listen(fmt.Sprintf(":%d", config.GetConfig().Client.Port))
}
