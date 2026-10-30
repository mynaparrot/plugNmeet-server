package models

import (
	"fmt"
	"os"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
)

// CreateAnalyticsArtifact creates a new artifact record for a generated analytics file.
// It saves the provided JSON data to a file in the artifact storage path and records the metadata in the database.
func (m *ArtifactModel) CreateAnalyticsArtifact(roomTableId uint64, jsonData []byte, log *logrus.Entry) (*dbmodels.RoomArtifact, error) {
	log = log.WithFields(logrus.Fields{
		"method":      "CreateAnalyticsArtifact",
		"roomTableId": roomTableId,
	})

	// Get room info
	roomInfo, err := m.ds.GetRoomInfoByTableId(roomTableId)
	if err != nil {
		return nil, fmt.Errorf("failed to get room info for room %d: %w", roomTableId, err)
	}
	if roomInfo == nil {
		return nil, fmt.Errorf("room not found for room %d", roomTableId)
	}

	// Generate a unique filename for the analytics JSON file.
	fileName := fmt.Sprintf("analytics-%s-%d.json", roomInfo.Sid, time.Now().UnixMilli())

	relativePath, absolutePath, err := m.buildPath(fileName, roomInfo.RoomId, plugnmeet.RoomArtifactType_MEETING_ANALYTICS)
	if err != nil {
		return nil, fmt.Errorf("failed to build artifact path: %w", err)
	}

	// Write the JSON data to the file
	err = os.WriteFile(absolutePath, jsonData, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write analytics file: %w", err)
	}
	log.Infof("wrote analytics file to %s", absolutePath)

	metadata := &plugnmeet.RoomArtifactMetadata{
		FileInfo: &plugnmeet.RoomArtifactFileInfo{
			FilePath: relativePath,
			FileSize: int64(len(jsonData)),
			MimeType: "application/json",
		},
	}

	artifact, err := m.createAndSaveArtifact(roomInfo.RoomId, roomInfo.Sid, roomTableId, plugnmeet.RoomArtifactType_MEETING_ANALYTICS, metadata, false, log)
	if err != nil {
		// If creating the artifact fails, we should try to remove the file we just created.
		log.WithError(err).Error("failed to create analytics artifact in database")
		if errRemove := os.Remove(absolutePath); errRemove != nil {
			log.WithError(errRemove).Errorf("CRITICAL: failed to remove analytics file at %s after DB error. The file exists without a DB record.", absolutePath)
		}
		return nil, err
	}

	return artifact, nil
}
