package config

import "time"

const (
	RecorderBot           = "RECORDER_BOT"
	RtmpBot               = "RTMP_BOT"
	IngressUserIdPrefix   = "ingres_"
	AgentUserUserIdPrefix = "pnm_agent-"
	TTSAgentUserIdPrefix  = "pnm_tts_agent-"
	SipUserIdPrefix       = "sip_"
	RecorderUserAuthName  = "PLUGNMEET_RECORDER_AUTH"

	// all the time.Sleep() values
	WaitBeforeTriggerOnAfterRoomEnded      = 10 * time.Second
	WaitBeforeAnalyticsStartProcessing     = 50 * time.Second
	WaitBeforeBreakoutRoomOnAfterRoomStart = 2 * time.Second
)
