package utils

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"gopkg.in/yaml.v3"
	"os"
)

func PrepareServer(c string) error {
	if config.AppCnf != nil {
		return nil
	}

	err := readYaml(c)
	if err != nil {
		return err
	}

	// set mysql factory connection
	factory.NewDbConnection()
	// set redis connection
	factory.NewRedisConnection()

	// we'll subscribe to redis channels now
	go controllers.SubscribeToWebsocketChannel()
	go controllers.StartScheduler()

	return nil
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
