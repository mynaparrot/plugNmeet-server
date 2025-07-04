package main

import (
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/routers"
	"github.com/mynaparrot/plugnmeet-server/version"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cli.VersionPrinter = func(c *cli.Command) {
		fmt.Printf("%s\n", c.Version)
	}

	app := &cli.Command{
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
	err := app.Run(context.Background(), os.Args)
	if err != nil {
		log.Fatalln(err)
	}
}

func startServer(ctx context.Context, c *cli.Command) error {
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

	appFactory, err := factory.NewAppFactory(appCnf)
	if err != nil {
		log.Fatalln(err)
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
		log.Infoln("exit requested, shutting down", "signal", sig)
		_ = rt.Shutdown()
	}()

	err = rt.Listen(fmt.Sprintf(":%d", appCnf.Client.Port))
	if err != nil {
		log.Fatalln(err)
	}
	return nil
}
