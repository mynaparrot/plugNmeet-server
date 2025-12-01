package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

// createChatTranslationUsageArtifact creates an artifact record for chat translation usage.
// It's designed to be called when a room ends.
func (m *ArtifactModel) createChatTranslationUsageArtifact(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) error {
	// 1. Atomically get usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetChatTranslationRoomUsage(m.ctx, roomId, true)
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
		// THE FIX: Use CharacterCountUsage instead of TokenUsage
		UsageDetails: &plugnmeet.RoomArtifactMetadata_CharacterCountUsage{
			CharacterCountUsage: &plugnmeet.RoomArtifactCharacterCountUsage{
				TotalCharacters: uint32(total),
				Breakdown:       usageMap,
			},
		},
	}

	// 3. Marshal metadata to JSON.
	// Use json.Marshal for the metadata, as protojson.Marshal is for protobuf messages directly.
	metadataBytes, err := protojson.Marshal(metadata)
	if err != nil {
		return err
	}

	// 4. Create the database record.
	artifact := &dbmodels.RoomArtifact{
		ArtifactId:   uuid.NewString(),
		RoomTableID:  roomTableId,
		RoomId:       roomId,
		Type:         plugnmeet.RoomArtifactType_CHAT_TRANSLATION,
		Metadata:     string(metadataBytes),
		CreationTime: time.Now().Unix(),
	}

	_, err = m.ds.CreateRoomArtifact(artifact)
	if err != nil {
		return err
	}

	// 5. Send webhook notification.
	m.sendWebhookNotification(ArtifactCreated, roomSid, artifact, metadata)
	log.Infof("successfully created chat translation artifact for room %s", roomId)

	// 6. Add to analytics
	m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_CHAT_TRANSLATION_TOTAL_USAGE, nil, &total)

	return nil
}
