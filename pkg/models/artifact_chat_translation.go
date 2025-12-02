package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// createChatTranslationUsageArtifact creates an artifact record for chat translation usage.
// It's designed to be called when a room ends.
func (m *ArtifactModel) createChatTranslationUsageArtifact(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) error {
	// Atomically get usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetChatTranslationRoomUsage(m.ctx, roomId, true)
	if err != nil {
		return err
	}

	if len(usageMap) == 0 {
		// No usage was recorded, so there's nothing to do.
		return nil
	}

	// Prepare the metadata message.
	total, _ := usageMap[redisservice.TotalUsageField]
	metadata := &plugnmeet.RoomArtifactMetadata{
		// THE FIX: Use CharacterCountUsage instead of TokenUsage
		UsageDetails: &plugnmeet.RoomArtifactMetadata_CharacterCountUsage{
			CharacterCountUsage: &plugnmeet.RoomArtifactCharacterCountUsage{
				TotalCharacters: uint32(total),
				Breakdown:       usageMap,
			},
		},
	}

	// save to database and send notification
	err = m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_CHAT_TRANSLATION_USAGE, metadata, log)
	if err != nil {
		return err
	}

	// Add to analytics
	m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_TOTAL_USAGE, nil, &total)

	return nil
}
