package models

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"google.golang.org/protobuf/encoding/protojson"
)

// CreateMeetingSummaryArtifact handles the business logic of creating a meeting summary artifact.
func (m *ArtifactModel) CreateMeetingSummaryArtifact(roomTableId uint64, summaryText string, promptTokens, completionTokens, totalTokens uint32, providerJobId, providerFileName string) error {
	// Get room info
	roomInfo, err := m.ds.GetRoomInfoByTableId(roomTableId)
	if err != nil {
		return fmt.Errorf("failed to get room info for room %d: %w", roomTableId, err)
	}
	if roomInfo == nil {
		return fmt.Errorf("room not found for room %d", roomTableId)
	}

	summaryFileName := fmt.Sprintf("summary_%d.txt", time.Now().Unix())
	// Construct the relative path and the full absolute path for writing.
	relativePath, absolutePath, err := m.buildPath(summaryFileName, roomInfo.RoomId, plugnmeet.RoomArtifactType_MEETING_SUMMARY)
	if err != nil {
		return err
	}

	// Write the file using the absolute path.
	err = os.WriteFile(absolutePath, []byte(summaryText), 0644)
	if err != nil {
		return fmt.Errorf("failed to write summary file: %w", err)
	}

	// Prepare metadata for the database using the universal protobuf message.
	metadata := &plugnmeet.RoomArtifactMetadata{
		ProviderJobInfo: &plugnmeet.RoomArtifactProviderJobInfo{
			JobId:    providerJobId,
			FileName: providerFileName,
		},
		FileInfo: &plugnmeet.RoomArtifactFileInfo{
			// Store the clean, relative path in the database.
			FilePath: relativePath,
			FileSize: int64(len(summaryText)),
			MimeType: "text/plain",
		},
		UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
			TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      totalTokens,
			},
		},
	}

	metadataBytes, err := protojson.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Create the artifact record in the database.
	artifact := &dbmodels.RoomArtifact{
		ArtifactId:   uuid.NewString(),
		RoomTableID:  roomTableId,
		RoomId:       roomInfo.RoomId,
		Type:         plugnmeet.RoomArtifactType_MEETING_SUMMARY,
		Metadata:     string(metadataBytes),
		CreationTime: time.Now().Unix(),
	}

	_, err = m.ds.CreateRoomArtifact(artifact)
	if err != nil {
		// If DB insertion fails, try to clean up the file we just wrote.
		_ = os.Remove(absolutePath)
		return fmt.Errorf("failed to create room artifact record: %w", err)
	}

	m.log.Infof("successfully created meeting summary artifact for room %s", roomInfo.RoomId)

	// Notify by webhook.
	m.sendWebhookNotification(ArtifactCreated, roomInfo.Sid, artifact, metadata)
	return nil
}
