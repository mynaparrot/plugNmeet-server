package helpers

import (
	"context"
	"os"

	infraNats "github.com/mynaparrot/plugnmeet-protocol/infra/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"gopkg.in/yaml.v3"
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

func PrepareServer(ctx context.Context, appCnf *config.AppConfig) error {
	// orm
	err := factory.NewDatabaseConnection(ctx, appCnf)
	if err != nil {
		return err
	}

	// set redis connection
	err = factory.NewRedisConnection(ctx, appCnf)
	if err != nil {
		return err
	}

	// nats
	nc, js, err := infraNats.NewNatsConnection(appCnf.NatsInfo)
	if err != nil {
		return err
	}
	appCnf.NatsConn = nc
	appCnf.JetStream = js

	// initialize nats Cache Service
	natsservice.InitNatsCacheService(appCnf, appCnf.Logger)

	return nil
}
