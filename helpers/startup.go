package helpers

import (
	"github.com/mynaparrot/plugnmeet-protocol/factory"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/temporary"
	"gopkg.in/yaml.v3"
	"os"
)

func ReadYamlConfigFile(file string) (*config.AppConfig, error) {
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	appCnf := new(config.AppConfig)
	err = yaml.Unmarshal(yamlFile, &appCnf)
	if err != nil {
		return nil, err
	}

	// get current working dir
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// set the root path
	appCnf.RootWorkingDir = wd

	return appCnf, err
}

func PrepareServer(appCnf *config.AppConfig) error {
	// orm
	err := temporary.NewDatabaseConnection(appCnf)
	if err != nil {
		return err
	}

	// set redis connection
	rds, err := factory.NewRedisConnection(appCnf.RedisInfo)
	if err != nil {
		return err
	}
	appCnf.RDS = rds

	return nil
}
