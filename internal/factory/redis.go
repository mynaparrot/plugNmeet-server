package factory

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
)

var RDB *redis.Client

func NewRedisConnection() {
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.AppCnf.RedisInfo.Host,
		Username: config.AppCnf.RedisInfo.Username,
		Password: config.AppCnf.RedisInfo.Password,
		DB:       config.AppCnf.RedisInfo.DBName,
	})

	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalln(err)
	}

	config.AppCnf.RDS = rdb
}

func SetRedisConnection(r *redis.Client) {
	_, err := r.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalln(err)
	}
	RDB = r
}
