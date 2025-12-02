package models

import (
	"fmt"
	"os"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

// CreateMeetingSummaryArtifact handles the business logic of creating a meeting summary artifact.
// It creates two separate artifacts: one for the downloadable file and one for the usage record.
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

	// --- Create the downloadable file artifact ---
	summaryFileName := fmt.Sprintf("summary_%d.txt", time.Now().Unix())
	relativePath, absolutePath, err := m.buildPath(summaryFileName, roomInfo.RoomId, plugnmeet.RoomArtifactType_MEETING_SUMMARY)
	if err != nil {
		return err
	}

	fileContent := fmt.Sprintf("Meeting Summary for: %s\n\n---\n\n%s", roomInfo.RoomId, summaryText)
	err = os.WriteFile(absolutePath, []byte(fileContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write summary file: %w", err)
	}

	fileMetadata := &plugnmeet.RoomArtifactMetadata{
		ProviderJobInfo: &plugnmeet.RoomArtifactProviderJobInfo{
			JobId:    providerJobId,
			FileName: providerFileName,
		},
		FileInfo: &plugnmeet.RoomArtifactFileInfo{
			FilePath: relativePath,
			FileSize: int64(len(fileContent)),
			MimeType: "text/plain",
		},
	}

	err = m.createAndSaveArtifact(roomInfo.RoomId, roomInfo.Sid, roomTableId, plugnmeet.RoomArtifactType_MEETING_SUMMARY, fileMetadata, log)
	if err != nil {
		log.WithError(err).Error("failed to create meeting summary file artifact")
	}

	// --- Create the non-deletable usage artifact ---
	usageMetadata := &plugnmeet.RoomArtifactMetadata{
		ProviderJobInfo: &plugnmeet.RoomArtifactProviderJobInfo{
			JobId:    providerJobId,
			FileName: providerFileName,
		},
		UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
			TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      totalTokens,
			},
		},
	}

	err = m.createAndSaveArtifact(roomInfo.RoomId, roomInfo.Sid, roomTableId, plugnmeet.RoomArtifactType_MEETING_SUMMARY_USAGE, usageMetadata, log)
	if err != nil {
		log.WithError(err).Error("failed to create meeting summary usage artifact")
	}

	return err
}
