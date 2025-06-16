package config

import "time"

const (
	RecorderBot                          = "RECORDER_BOT"
	RtmpBot                              = "RTMP_BOT"
	IngressUserIdPrefix                  = "ingres_"
	RecorderUserAuthName                 = "PLUGNMEET_RECORDER_AUTH"
	MaxPreloadedWhiteboardFileSize int64 = 5 * 1000000 // limit to 5MB

	// all the time.Sleep() values
	WaitBeforeTriggerOnAfterRoomEnded        = 10 * time.Second
	WaitBeforeSpeechServicesOnAfterRoomEnded = 3 * time.Second
	WaitBeforeBreakoutRoomOnAfterRoomStart   = 2 * time.Second
	WaitBeforeAnalyticsStartProcessing       = 40 * time.Second
	MaxDurationWaitBeforeCleanRoomWebhook    = 1 * time.Minute

	DefaultWebhookQueueSize = 200
)
