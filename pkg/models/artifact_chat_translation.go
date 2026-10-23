package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// createChatTranslationUsageArtifact creates an artifact record for chat translation usage.
// It's designed to be called when a room ends.
// sourceRoomId is the room where the usage occurred (usage hash is harvested
// from its Redis keys), while ownerRoomId/ownerRoomSid/ownerTableId identify
// the room that owns the resulting artifact (parent room for breakout activity).
func (m *ArtifactModel) createChatTranslationUsageArtifact(sourceRoomId, ownerRoomId, ownerRoomSid string, sourceTableId, ownerTableId uint64, isBreakout bool, log *logrus.Entry) error {
	// Atomically get usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetChatTranslationRoomUsage(m.ctx, sourceRoomId, true)
	if err != nil {
		return err
	}

	if len(usageMap) == 0 {
		// No usage was recorded, so there's nothing to do.
		return nil
	}

	// Prepare the metadata message.
	total, _ := usageMap[redisservice.TotalUsageField]

	// Get pricing & calculate cost
	var cost float64
	pricing, err := m.app.Insights.GetServiceModelPricing(insights.ServiceTypeTranslation, "default")
	if err == nil {
		// price is per million characters
		cost = (float64(total) / 1000000) * pricing.PricePerMillionCharacters
	} else {
		log.WithError(err).Warn("could not calculate cost for translation")
	}

	metadata := &plugnmeet.RoomArtifactMetadata{
		UsageDetails: &plugnmeet.RoomArtifactMetadata_CharacterCountUsage{
			CharacterCountUsage: &plugnmeet.RoomArtifactCharacterCountUsage{
				TotalCharacters:              uint32(total),
				Breakdown:                    usageMap,
				TotalCharactersEstimatedCost: roundAndPointer(cost, 6),
			},
		},
	}
	m.setUsageArtifactProvenance(metadata, sourceTableId, isBreakout)

	// save to database and send notification
	_, err = m.createAndSaveArtifact(ownerRoomId, ownerRoomSid, ownerTableId, plugnmeet.RoomArtifactType_CHAT_TRANSLATION_USAGE, metadata, false, log)
	if err != nil {
		return err
	}

	// Add to analytics
	m.HandleAnalyticsEvent(ownerRoomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_TOTAL_USAGE, nil, &total)

	return nil
}
