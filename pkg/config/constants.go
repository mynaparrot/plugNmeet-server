package config

import (
	"strings"
	"time"
)

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
// for the given primary userId ("[userID]-native"). Returns the userId unchanged
// if the "-native" suffix is already present.
func GetNativeTwinIdentity(userId string) string {
	if strings.HasSuffix(userId, NativeTwinIdentitySuffix) {
		return userId
	}
	return userId + NativeTwinIdentitySuffix
}

// PrimaryIdentityFromNative strips the native twin suffix if present, returning
// the primary user id ("[userID]-native" -> "[userID]"). If the suffix is not
// present the identity is returned unchanged.
func PrimaryIdentityFromNative(identity string) string {
	return strings.TrimSuffix(identity, NativeTwinIdentitySuffix)
}
