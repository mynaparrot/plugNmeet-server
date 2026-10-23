package models

import (
	"fmt"
	"os"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// CreateMeetingSummaryArtifact handles the business logic of creating a meeting summary artifact.
// It creates a file artifact, then a usage artifact that references the file.
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

	// --- Create the downloadable file artifact FIRST ---
	summaryFileName := fmt.Sprintf("summary_%s-%d.txt", roomInfo.Sid, time.Now().UnixMilli())
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

	// Create the file artifact and get its ID.
	fileArtifact, err := m.createAndSaveArtifact(roomInfo.RoomId, roomInfo.Sid, roomTableId, plugnmeet.RoomArtifactType_MEETING_SUMMARY, fileMetadata, true, log)
	if err != nil {
		return fmt.Errorf("failed to create meeting summary file artifact: %w", err)
	}

	// --- Create the non-deletable usage artifact, linking it to the file artifact ---

	// 1. Get pricing & calculate cost
	var promptCost, completionCost, totalCost float64
	_, service, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeMeetingSummarizing)
	if err != nil {
		log.WithError(err).Error("could not get service config for meeting_summarizing")
	} else if service.Options != nil {
		modelName := "default"
		if model, ok := service.Options["summarize_model"].(string); ok {
			modelName = model
		}
		pricing, err := m.app.Insights.GetServiceModelPricing(insights.ServiceTypeMeetingSummarizing, modelName)
		if err == nil {
			promptCost = (float64(promptTokens) / 1000000) * pricing.InputPricePerMillionTokens
			completionCost = (float64(completionTokens) / 1000000) * pricing.OutputPricePerMillionTokens
			totalCost = promptCost + completionCost
		} else {
			log.WithError(err).Warnf("could not calculate cost for meeting_summarizing model %s", modelName)
		}
	}

	// 2. Prepare usage metadata
	usageMetadata := &plugnmeet.RoomArtifactMetadata{
		ProviderJobInfo: &plugnmeet.RoomArtifactProviderJobInfo{
			JobId:    providerJobId,
			FileName: providerFileName,
		},
		UsageDetails: &plugnmeet.RoomArtifactMetadata_TokenUsage{
			TokenUsage: &plugnmeet.RoomArtifactTokenUsage{
				PromptTokens:                  promptTokens,
				CompletionTokens:              completionTokens,
				TotalTokens:                   totalTokens,
				PromptTokensEstimatedCost:     roundAndPointer(promptCost, 6),
				CompletionTokensEstimatedCost: roundAndPointer(completionCost, 6),
				TotalTokensEstimatedCost:      roundAndPointer(totalCost, 6),
			},
		},
		// Link this usage artifact back to the file artifact.
		ReferenceArtifactId: &fileArtifact.ArtifactId,
	}

	// Extends the usage-artifact ownership contract: if the summarizing activity
	// occurred in a breakout room, the usage artifact is owned by the parent room.
	usageRoomId, usageRoomSid, usageTableId := roomInfo.RoomId, roomInfo.Sid, roomTableId
	isBreakout := roomInfo.IsBreakoutRoom != 0
	if isBreakout {
		parentRoomId, parentTableId, perr := ParseBreakoutParentRoomRef(roomInfo.ParentRoomID)
		if perr != nil {
			return fmt.Errorf("failed to parse parent room reference of %s for meeting summary usage artifact: %w", roomInfo.RoomId, perr)
		}

		// Resolve by table id to target the exact parent session, never by the
		// ambiguous roomId (which may have multiple room_info rows).
		parentInfo, perr := m.ds.GetRoomInfoByTableId(parentTableId)
		switch {
		case perr != nil:
			return fmt.Errorf("failed to resolve parent room of %s for meeting summary usage artifact: %w", roomInfo.RoomId, perr)
		case parentInfo == nil:
			return fmt.Errorf("parent room table id %d not found for meeting summary usage artifact", parentTableId)
		case parentInfo.RoomId != parentRoomId:
			return fmt.Errorf("parent room reference %s does not match resolved row %s for meeting summary usage artifact", parentRoomId, parentInfo.RoomId)
		}
		usageRoomId, usageRoomSid, usageTableId = parentInfo.RoomId, parentInfo.Sid, parentInfo.ID
	}
	m.setUsageArtifactProvenance(usageMetadata, roomInfo.ID, isBreakout)

	// 3. Create the usage artifact.
	_, err = m.createAndSaveArtifact(usageRoomId, usageRoomSid, usageTableId, plugnmeet.RoomArtifactType_MEETING_SUMMARY_USAGE, usageMetadata, true, log)
	if err != nil {
		log.WithError(err).Error("failed to create meeting summary usage artifact")
	}

	return err
}
