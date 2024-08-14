package config

import "time"

const (
	RecorderBot                          = "RECORDER_BOT"
	RtmpBot                              = "RTMP_BOT"
	MaxPreloadedWhiteboardFileSize int64 = 5 * 1000000 // limit to 5MB

	// all the time.Sleep() values
	WaitBeforeTriggerOnAfterRoomEnded        = 5 * time.Second
	WaitBeforeSpeechServicesOnAfterRoomEnded = 3 * time.Second
	WaitBeforeBreakoutRoomOnAfterRoomStart   = 2 * time.Second
	WaitBeforeAnalyticsStartProcessing       = 30 * time.Second
	MaxDurationWaitBeforeCleanRoomWebhook    = 1 * time.Minute
	WaitDurationIfRoomInProgress             = 1 * time.Second

	DefaultWebsocketQueueSize = 1000
	DefaultWebhookQueueSize   = 200

	UserWebsocketChannel       = "plug-n-meet-user-websocket"
	WhiteboardWebsocketChannel = "plug-n-meet-whiteboard-websocket"
	SystemWebsocketChannel     = "plug-n-meet-system-websocket"
	RecorderChannel            = "plug-n-meet-recorder"
)
