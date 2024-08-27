package redisservice

const (
	BlockedUsersList = Prefix + "block_users_list:"
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
