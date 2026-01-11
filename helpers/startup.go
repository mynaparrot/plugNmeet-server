package helpers

import (
	"context"
	"os"

	infraDb "github.com/mynaparrot/plugnmeet-protocol/infra/database"
	infraNats "github.com/mynaparrot/plugnmeet-protocol/infra/nats"
	infraRedis "github.com/mynaparrot/plugnmeet-protocol/infra/redis"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
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
	db, err := infraDb.NewDatabaseConnection(ctx, appCnf.DatabaseInfo, appCnf.Client.Debug, appCnf.Logger)
	if err != nil {
		return err
	}
	appCnf.DB = db

	// set redis connection
	rds, err := infraRedis.NewRedisConnection(ctx, appCnf.RedisInfo, appCnf.Logger)
	if err != nil {
		return err
	}
	appCnf.RDS = rds

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
