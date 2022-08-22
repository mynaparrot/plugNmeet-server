package main

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
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
		Version: Version,
	}
	err := app.Run(os.Args)
	if err != nil {
		log.Fatalln(err)
	}
}

func startServer(c *cli.Context) error {
	err := readYaml(c.String("config"))
	if err != nil {
		return err
	}

	// set mysql factory connection
	factory.NewDbConnection()
	factory.SetDBConnection(config.AppCnf.DB)
	defer config.AppCnf.DB.Close()

	// set redis connection
	factory.NewRedisConnection()
	factory.SetRedisConnection(config.AppCnf.RDS)
	defer config.AppCnf.RDS.Close()

	// we'll subscribe to redis channels now
	go controllers.SubscribeToWebsocketChannel()
	go controllers.StartScheduler()

	router := Router()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		log.Infoln("exit requested, shutting down", "signal", sig)
		_ = router.Shutdown()
		log.Exit(1)
	}()

	return router.Listen(fmt.Sprintf(":%d", config.AppCnf.Client.Port))
}

func readYaml(filename string) error {
	var appConfig config.AppConfig
	yamlFile, err := os.ReadFile(filename)

	if err != nil {
		return err
	}

	err = yaml.Unmarshal(yamlFile, &appConfig)
	if err != nil {
		return err
	}
	config.SetAppConfig(&appConfig)

	return nil
}
