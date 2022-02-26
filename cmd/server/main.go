package main

import (
	"flag"
	"fmt"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/controllers"
	"github.com/mynaparrot/plugNmeet/internal/factory"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configFile := flag.String("config", "config.yaml", "Configuration file")
	flag.Parse()
	readYaml(configFile)

	// set mysql factory connection
	factory.NewDbConnection()
	factory.SetDBConnection(config.AppCnf.DB)
	defer config.AppCnf.DB.Close()

	// set redis connection
	factory.NewRedisConnection()
	factory.SetRedisConnection(config.AppCnf.RDS)
	defer config.AppCnf.RDS.Close()

	// we'll subscribe to redis channels now
	go controllers.SubscribeToRecorderChannel()
	go controllers.SubscribeToWebsocketChannel()

	router := Router()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		log.Infoln("exit requested, shutting down", "signal", sig)
		_ = router.Shutdown()
	}()

	err := router.Listen(fmt.Sprintf(":%d", config.AppCnf.Client.Port))
	if err != nil {
		log.Fatalln(err)
	}

	log.Infoln("Running cleanup tasks...")
	log.Exit(1)
}

func readYaml(filename *string) {
	var appConfig config.AppConfig
	yamlFile, err := ioutil.ReadFile(*filename)

	if err != nil {
		log.Fatalln(err)
	}

	err = yaml.Unmarshal(yamlFile, &appConfig)
	if err != nil {
		log.Fatalln(err)
	}
	config.SetAppConfig(&appConfig)
}
