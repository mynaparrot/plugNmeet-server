package redisservice

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

const (
	Prefix          = "pnm:"
	DefaultTTL      = time.Hour * 24
	TotalUsageField = "total_usage"
)

type RedisService struct {
	ctx              context.Context
	rc               *redis.Client
	unlockScriptExec *redis.Script
	renewScriptExec  *redis.Script
	logger           *logrus.Entry
}

type Args struct {
	fx.In
	Ctx    context.Context
	Rc     *redis.Client
	Logger *logrus.Logger
}

func New(args Args) *RedisService {
	return &RedisService{
		ctx:              args.Ctx,
		rc:               args.Rc,
		unlockScriptExec: redis.NewScript(unlockScript),
		renewScriptExec:  redis.NewScript(renewScript),
		logger:           args.Logger.WithField("service", "redis"),
	}
}

func (s *RedisService) GetRedisClient() *redis.Client {
	return s.rc
}

func (s *RedisService) DeleteKeys(allKeys []string) error {
	_, err := s.rc.Del(s.ctx, allKeys...).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) ScanKeys(pattern string) ([]string, error) {
	var cursor uint64
	var allKeys []string

	for {
		keys, nextCursor, err := s.rc.Scan(s.ctx, cursor, pattern, 0).Result()
		if err != nil {
			return nil, err
		}
		allKeys = append(allKeys, keys...)
		if nextCursor == 0 {
			break
		}
		cursor = nextCursor
	}
	return allKeys, nil
}
