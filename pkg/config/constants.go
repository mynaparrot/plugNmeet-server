package config

import (
	"strconv"
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

	// separator for comma-joined selected option ids ("1,2,3") in poll storage & analytics
	PollOptionIdsSeparator = ","

	// MaxPollDurationSeconds caps poll auto-close duration at 60 minutes.
	MaxPollDurationSeconds = 3600
	// PollAutoClosedBy is the closed_by marker for polls auto-closed at expiry.
	PollAutoClosedBy = "pnm_poll_auto_close"

	// MsgRoomIdRequired is the plain error text when an Auth API request omits room_id.
	MsgRoomIdRequired = "missing required field room_id"
	// ExternalApiUserId is the created_by marker for polls pushed via the Auth API without a user_id.
	ExternalApiUserId = "external-api"
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

// JoinPollOptionIds joins selected option ids into a comma-joined string ("1,2,3").
func JoinPollOptionIds(ids []uint64) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatUint(id, 10)
	}
	return strings.Join(out, PollOptionIdsSeparator)
}

// SplitPollOptionIds splits a comma-joined option-id string back into ids.
func SplitPollOptionIds(s string) ([]uint64, error) {
	parts := strings.Split(s, PollOptionIdsSeparator)
	ids := make([]uint64, 0, len(parts))
	for _, p := range parts {
		id, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}
