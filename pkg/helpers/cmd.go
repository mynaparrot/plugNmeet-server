package helpers

import (
	"github.com/mynaparrot/plugnmeet-protocol/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/tmp"
	"gopkg.in/yaml.v3"
	"os"
)

func PrepareServer(c string) error {
	if config.AppCnf != nil {
		return nil
	}

	cnf, err := ReadConfig(c)
	if err != nil {
		return err
	}
	config.SetAppConfig(cnf)

	// orm
	err = tmp.NewDatabaseConnection(config.AppCnf)
	if err != nil {
		return err
	}

	// set mysql factory connection
	db, err := factory.NewDbConnection(config.AppCnf.DatabaseInfo)
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

func ReadConfig(cnfFile string) (*config.AppConfig, error) {
	return readYaml(cnfFile)
}

func readYaml(filename string) (*config.AppConfig, error) {
	yamlFile, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	appCnf := new(config.AppConfig)
	err = yaml.Unmarshal(yamlFile, &appCnf)
	if err != nil {
		return nil, err
	}

	return appCnf, err
}
