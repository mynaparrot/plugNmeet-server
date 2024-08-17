package redisservice

import (
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"time"
)

const (
	BlockedUsersList      = Prefix + "block_users_list:"
	ActiveRoomUsers       = Prefix + "activeRoom:%s:users"
	RoomWithUsersMetadata = Prefix + "roomWithUsersMetadata:%s"
)

// AddUserToBlockList will add users to blocklist, we're using redis set
func (s *RedisService) AddUserToBlockList(roomId, userId string) (int64, error) {
	key := BlockedUsersList + roomId
	return s.rc.SAdd(s.ctx, key, userId).Result()
}

// IsUserExistInBlockList to check if the user is present in the blacklist
func (s *RedisService) IsUserExistInBlockList(roomId, userId string) bool {
	key := BlockedUsersList + roomId
	exist, err := s.rc.SIsMember(s.ctx, key, userId).Result()
	if err != nil {
		return false
	}
	return exist
}

// DeleteRoomBlockList to completely delete blocklist set to provide roomId
func (s *RedisService) DeleteRoomBlockList(roomId string) (int64, error) {
	key := BlockedUsersList + roomId
	return s.rc.Del(s.ctx, key).Result()
}

// ManageActiveUsersList will use redis sorted sets to manage active users
// task = add | del | get | fetchAll | delList (to delete this entire list)
func (s *RedisService) ManageActiveUsersList(roomId, userId, task string, timeStamp int64) ([]redis.Z, error) {
	if timeStamp == 0 {
		timeStamp = time.Now().Unix()
	}
	key := fmt.Sprintf(ActiveRoomUsers, roomId)
	var out []redis.Z
	var err error

	switch task {
	case "add":
		_, err = s.rc.ZAdd(s.ctx, key, redis.Z{
			Score:  float64(timeStamp),
			Member: userId,
		}).Result()
		if err != nil {
			return out, err
		}
	case "del":
		_, err = s.rc.ZRem(s.ctx, key, userId).Result()
		if err != nil {
			return out, err
		}
	case "delList":
		// this will delete this key completely
		// we'll trigger this when the session was ended
		_, err = s.rc.Del(s.ctx, key).Result()
		if err != nil {
			return out, err
		}
	case "get":
		result, err := s.rc.ZScore(s.ctx, key, userId).Result()
		switch {
		case errors.Is(err, redis.Nil):
			return out, nil
		case err != nil:
			return out, err
		case result == 0:
			return out, nil
		}

		out = append(out, redis.Z{
			Member: userId,
			Score:  result,
		})
	case "fetchAll":
		out, err = s.rc.ZRandMemberWithScores(s.ctx, key, -1).Result()
		if err != nil {
			return out, err
		}
	}

	return out, nil
}

// ManageRoomWithUsersMetadata can be used to store user metadata in redis
// this way we'll be able to access info quickly
// task = add | del | get | delList (to delete this entire list)
func (s *RedisService) ManageRoomWithUsersMetadata(roomId, userId, task, metadata string) (string, error) {
	key := fmt.Sprintf(RoomWithUsersMetadata, roomId)
	switch task {
	case "add":
		_, err := s.rc.HSet(s.ctx, key, userId, metadata).Result()
		if err != nil {
			return "", err
		}
		return "", nil
	case "get":
		result, err := s.rc.HGet(s.ctx, key, userId).Result()
		switch {
		case errors.Is(err, redis.Nil):
			return "", nil
		case err != nil:
			return "", err
		}
		return result, nil
	case "del":
		_, err := s.rc.HDel(s.ctx, key, userId).Result()
		if err != nil {
			return "", err
		}
		return "", nil
	case "delList":
		// this will delete this key completely
		// we'll trigger this when the session was ended
		_, err := s.rc.Del(s.ctx, key).Result()
		if err != nil {
			return "", err
		}
		return "", nil
	}
	return "", errors.New("invalid task")
}
