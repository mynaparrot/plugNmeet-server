package redisservice

import (
	"context"
	"github.com/redis/go-redis/v9"
)

const (
	Prefix = "pnm:"
)

type RedisService struct {
	rc  *redis.Client
	ctx context.Context
}

func New(rc *redis.Client) *RedisService {
	return &RedisService{
		rc:  rc,
		ctx: context.Background(),
	}
}
