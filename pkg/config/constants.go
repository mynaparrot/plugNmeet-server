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
	HeaderRoomId          = "Room-Id"
	UploadFileTempDir     = "tmp"
	// NativeTwinIdentitySuffix is appended to a hybrid user's userId to form
	// the LiveKit identity of their publish-only native twin: "[userID]-native".
	NativeTwinIdentitySuffix = "-native"

	// all the time.Sleep() values
	WaitBeforeTriggerOnAfterRoomEnded      = 10 * time.Second
	WaitBeforeAnalyticsStartProcessing     = 50 * time.Second
	WaitBeforeBreakoutRoomOnAfterRoomStart = 2 * time.Second
)

// GetNativeTwinIdentity returns the LiveKit identity of the hybrid native twin
// for the given primary userId ("[userID]-native").
func GetNativeTwinIdentity(userId string) string {
	return userId + NativeTwinIdentitySuffix
}
