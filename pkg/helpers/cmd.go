package helpers

import (
	"github.com/mynaparrot/plugnmeet-protocol/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
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
	db, err := factory.NewDbConnection(config.AppCnf.MySqlInfo)
	if err != nil {
		return err
	}
	config.AppCnf.DB = db

	// set redis connection
	rds, err := factory.NewRedisConnection(config.AppCnf.RedisInfo)
	if err != nil {
		return err
	}
	config.AppCnf.RDS = rds

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
