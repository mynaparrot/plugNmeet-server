package config

import "time"

const (
	RECORDER_BOT                                  = "RECORDER_BOT"
	RTMP_BOT                                      = "RTMP_BOT"
	MAX_PRELOADED_WHITEBOARD_FILE_SIZE      int64 = 5 * 1000000 // limit to 5MB
	WAIT_BEFORE_TRIGGER_ON_AFTER_ROOM_ENDED       = 5 * time.Second
)
