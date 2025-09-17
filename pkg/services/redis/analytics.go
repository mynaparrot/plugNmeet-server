package redisservice

import (
	"time"
)

func (s *RedisService) AnalyticsGetKeyType(key string) (string, error) {
	return s.rc.Type(s.ctx, key).Result()
}

func (s *RedisService) AddAnalyticsHSETType(key string, val map[string]string) error {
	_, err := s.rc.HSet(s.ctx, key, val).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetAnalyticsAllHashTypeVals(key string) (map[string]string, error) {
	return s.rc.HGetAll(s.ctx, key).Result()
}

func (s *RedisService) IncrementAnalyticsVal(key string, val int64) error {
	_, err := s.rc.IncrBy(s.ctx, key, val).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) AddAnalyticsStringType(key, val string) error {
	_, err := s.rc.Set(s.ctx, key, val, time.Duration(0)).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) GetAnalyticsStringTypeVal(key string) (string, error) {
	return s.rc.Get(s.ctx, key).Result()
}

func (s *RedisService) AddAnalyticsUser(key string, val map[string]string) error {
	_, err := s.rc.HSet(s.ctx, key, val).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) AnalyticsGetAllUsers(key string) (map[string]string, error) {
	result, err := s.rc.HGetAll(s.ctx, key).Result()
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *RedisService) AnalyticsDeleteKeys(allKeys []string) error {
	_, err := s.rc.Del(s.ctx, allKeys...).Result()
	if err != nil {
		return err
	}
	return nil
}

func (s *RedisService) AnalyticsScanKeys(pattern string) ([]string, error) {
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
