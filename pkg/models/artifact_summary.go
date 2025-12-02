package models

import (
	"fmt"
	"os"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

// CreateMeetingSummaryArtifact handles the business logic of creating a meeting summary artifact.
func (m *ArtifactModel) CreateMeetingSummaryArtifact(roomTableId uint64, summaryText string, promptTokens, completionTokens, totalTokens uint32, providerJobId, providerFileName string, log *logrus.Entry) error {
	log = log.WithField("method", "CreateMeetingSummaryArtifact")

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

	// Add a header to the summary text with the room ID.
	fileContent := fmt.Sprintf("Meeting Summary for: %s\n\n---\n\n%s", roomInfo.RoomId, summaryText)

	// Write the file using the absolute path.
	err = os.WriteFile(absolutePath, []byte(fileContent), 0644)
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
			FileSize: int64(len(fileContent)),
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

	// save to database and send notification
	return m.createAndSaveArtifact(roomInfo.RoomId, roomInfo.Sid, roomTableId, plugnmeet.RoomArtifactType_MEETING_SUMMARY, metadata, log)
}
