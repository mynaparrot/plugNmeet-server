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

// createSpeechTranscriptionUsageArtifact creates an artifact record for a speech transcription session.
// It's designed to be called when a room ends.
func (m *ArtifactModel) createSpeechTranscriptionUsageArtifact(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) error {
	// 1. Atomically get usage data from Redis and clean up the key.
	usageMap, err := m.rs.GetTranscriptionRoomUsage(m.ctx, roomId, true)
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
		UsageDetails: &plugnmeet.RoomArtifactMetadata_DurationUsage{
			DurationUsage: &plugnmeet.RoomArtifactDurationUsage{
				DurationSec: uint32(total),
				Breakdown:   usageMap,
			},
		},
	}

	// 3. Marshal metadata to JSON.
	metadataBytes, err := protojson.Marshal(metadata)
	if err != nil {
		return err
	}

	// 4. Create the database record.
	artifact := &dbmodels.RoomArtifact{
		ArtifactId:   uuid.NewString(),
		RoomTableID:  roomTableId,
		RoomId:       roomId,
		Type:         plugnmeet.RoomArtifactType_SPEECH_TRANSCRIPTION,
		Metadata:     string(metadataBytes),
		CreationTime: time.Now().Unix(),
	}

	_, err = m.ds.CreateRoomArtifact(artifact)
	if err != nil {
		return err
	}

	// 5. Send webhook notification.
	m.sendWebhookNotification(ArtifactCreated, roomSid, artifact, metadata)

	// 6. Add to analytics
	m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_TRANSCRIPTION_TOTAL_USAGE, nil, &total)

	log.Infof("successfully created speech transcription artifact for room %s", roomId)

	return nil
}
