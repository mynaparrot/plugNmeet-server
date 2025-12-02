package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// createSynthesizedSpeechUsageArtifact creates an artifact record for synthesized speech usage.
// It's designed to be called when a room ends.
func (m *ArtifactModel) createSynthesizedSpeechUsageArtifact(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) error {
	// 1. Atomically get usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetTTSServiceRoomUsage(m.ctx, roomId, true)
	if err != nil {
		return err
	}

	if len(usageMap) == 0 {
		// No usage was recorded, so there's nothing to do.
		return nil
	}

	// 2. Prepare the metadata message.
	total, _ := usageMap[redisservice.TotalUsageField]
	metadata := &plugnmeet.RoomArtifactMetadata{
		UsageDetails: &plugnmeet.RoomArtifactMetadata_CharacterCountUsage{
			CharacterCountUsage: &plugnmeet.RoomArtifactCharacterCountUsage{
				TotalCharacters: uint32(total),
				Breakdown:       usageMap,
			},
		},
	}

	m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_SYNTHESIZED_SPEECH_TOTAL_USAGE, nil, &total)

	return m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_SYNTHESIZED_SPEECH_USAGE, metadata, log)
}
