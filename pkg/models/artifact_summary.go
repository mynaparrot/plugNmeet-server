package models

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
)

type MeetingSummaryArtifactMetadata struct {
	SummaryFilePath  string                            `json:"summary_file_path"`
	FileSize         int                               `json:"file_size"`
	ProviderJobId    string                            `json:"provider_job_id"`
	ProviderFileName string                            `json:"provider_file_name"`
	TokenUsage       *plugnmeet.RoomArtifactTokenUsage `json:"token_usage"`
}

// CreateMeetingSummaryArtifact handles the business logic of creating a meeting summary artifact.
func (m *ArtifactModel) CreateMeetingSummaryArtifact(roomTableId uint64, summaryText string, promptTokens, completionTokens, totalTokens uint32, providerJobId, providerFileName string) error {
	// 1. Get room info to get the primary key
	roomInfo, err := m.ds.GetRoomInfoByTableId(roomTableId)
	if err != nil {
		return fmt.Errorf("failed to get room info for room %d: %w", roomTableId, err)
	}
	if roomInfo == nil {
		return fmt.Errorf("room not found for room %d", roomTableId)
	}

	// 2. Save the summary to a file
	artifactDir := filepath.Join(*m.app.ArtifactsSettings.StoragePath, strings.ToLower(plugnmeet.RoomArtifactType_MEETING_SUMMARY.String()), roomInfo.RoomId)
	err = os.MkdirAll(artifactDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create artifact directory: %w", err)
	}

	summaryFileName := fmt.Sprintf("summary_%d.txt", time.Now().Unix())
	summaryFilePath := filepath.Join(artifactDir, summaryFileName)

	err = os.WriteFile(summaryFilePath, []byte(summaryText), 0644)
	if err != nil {
		return fmt.Errorf("failed to write summary file: %w", err)
	}

	// 3. Prepare metadata for the database using the new structs
	metadata := MeetingSummaryArtifactMetadata{
		SummaryFilePath:  summaryFilePath,
		FileSize:         len(summaryText),
		ProviderJobId:    providerJobId,
		ProviderFileName: providerFileName,
		TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
			Prompt:     promptTokens,
			Completion: completionTokens,
			Total:      &totalTokens,
		},
	}

	metadataBytes, _ := json.Marshal(&metadata)

	// 4. Create the artifact record in the database
	artifact := &dbmodels.RoomArtifact{
		ArtifactId:   uuid.NewString(),
		RoomTableID:  roomInfo.ID,
		RoomId:       roomInfo.RoomId,
		Type:         plugnmeet.RoomArtifactType_MEETING_SUMMARY,
		Metadata:     string(metadataBytes),
		CreationTime: time.Now().Unix(),
	}

	_, err = m.ds.CreateRoomArtifact(artifact)
	if err != nil {
		// If DB insertion fails, try to clean up the file we just wrote.
		_ = os.Remove(summaryFilePath)
		return fmt.Errorf("failed to create room artifact record: %w", err)
	}

	m.log.Infof("successfully created meeting summary artifact for room %s", roomInfo.RoomId)

	// notify by webhook
	m.sendWebhookNotification("artifact_created", roomInfo.Sid, artifact, metadata.TokenUsage)
	return nil
}
