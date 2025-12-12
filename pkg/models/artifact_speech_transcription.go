package models

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// createSpeechTranscriptionUsageArtifact creates an artifact record for a speech transcription session.
func (m *ArtifactModel) createSpeechTranscriptionUsageArtifact(roomId, roomSid string, roomTableId uint64, fileArtifactId *string, log *logrus.Entry) error {
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

	// 3. Get pricing & calculate cost
	var cost float64
	pricing, err := m.app.Insights.GetServiceModelPricing(insights.ServiceTypeTranscription, "default")
	if err == nil {
		// Convert the hourly price to a per-second price first for a clean calculation.
		pricePerSecond := pricing.PricePerHour / 3600
		cost = float64(total) * pricePerSecond
	} else {
		log.WithError(err).Warn("could not calculate cost for transcription")
	}

	metadata := &plugnmeet.RoomArtifactMetadata{
		UsageDetails: &plugnmeet.RoomArtifactMetadata_DurationUsage{
			DurationUsage: &plugnmeet.RoomArtifactDurationUsage{
				DurationSec:              uint32(total),
				Breakdown:                usageMap,
				DurationSecEstimatedCost: roundAndPointer(cost, 6),
			},
		},
		// Link this usage artifact back to the file artifact.
		ReferenceArtifactId: fileArtifactId,
	}

	// Create and save the artifact for chat interactions.
	_, err = m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_SPEECH_TRANSCRIPTION_USAGE, metadata, false, log)
	if err != nil {
		log.WithError(err).Error("failed to create speech transcription usage artifact")
	}

	// 6. Add to analytics
	m.HandleAnalyticsEvent(roomId, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INSIGHTS_TRANSCRIPTION_TOTAL_USAGE, nil, &total)

	log.Infof("successfully created speech transcription usage artifact for room %s", roomId)

	return nil
}

// formatVTTTimestamp converts a duration into the required HH:MM:SS.mmm format.
func formatVTTTimestamp(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	milliseconds := int(d.Milliseconds() % 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, milliseconds)
}

// createSpeechTranscriptionFileArtifact creates a downloadable VTT artifact file
func (m *ArtifactModel) createSpeechTranscriptionFileArtifact(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) (artifactId *string, err error) {
	// 1. Get all transcription chunks from NATS KV.
	chunks, err := m.natsService.GetTranscriptionChunks(roomId)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil // No chunks stored.
	}

	// 2. Clean up the NATS bucket.
	m.natsService.DeleteTranscriptionBucket(roomId)

	// 3. Sort keys chronologically.
	keys := make([]string, 0, len(chunks))
	for k := range chunks {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 4. Format the chunks into a VTT file.
	var fileContent strings.Builder
	fileContent.WriteString("WEBVTT\n\n")
	fileContent.WriteString(fmt.Sprintf("NOTE Transcription for meeting: %s\n\n", roomId))

	var firstTimestamp int64 = -1
	var previousEndTime time.Duration

	for i, key := range keys {
		var chunk natsservice.TranscriptionChunk
		if err := json.Unmarshal(chunks[key], &chunk); err != nil {
			continue // Skip corrupted data
		}

		ts, _ := strconv.ParseInt(key, 10, 64)
		if firstTimestamp == -1 {
			firstTimestamp = ts
		}

		elapsedTime := time.Duration(ts - firstTimestamp)
		var startTime time.Duration
		if i > 0 {
			startTime = previousEndTime
		}

		vttStartTime := formatVTTTimestamp(startTime)
		vttEndTime := formatVTTTimestamp(elapsedTime)

		fileContent.WriteString(fmt.Sprintf("%d\n", i+1))
		fileContent.WriteString(fmt.Sprintf("%s --> %s\n", vttStartTime, vttEndTime))
		fileContent.WriteString(fmt.Sprintf("<v %s>%s\n\n", chunk.Name, chunk.Text))

		previousEndTime = elapsedTime
	}

	if fileContent.Len() <= 40 {
		return nil, nil
	}

	// 5. Build the file path and write the file.
	fileName := fmt.Sprintf("transcription_%d.vtt", time.Now().Unix())
	relativePath, absolutePath, err := m.buildPath(fileName, roomId, plugnmeet.RoomArtifactType_SPEECH_TRANSCRIPTION)
	if err != nil {
		return nil, err
	}
	err = os.WriteFile(absolutePath, []byte(fileContent.String()), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write transcription file: %w", err)
	}

	// 6. Prepare the metadata for the file artifact.
	metadata := &plugnmeet.RoomArtifactMetadata{
		FileInfo: &plugnmeet.RoomArtifactFileInfo{
			FilePath: relativePath,
			FileSize: int64(fileContent.Len()),
			MimeType: "text/vtt",
		},
	}

	// 7. Create the file artifact and get its ID.
	fileArtifact, err := m.createAndSaveArtifact(roomId, roomSid, roomTableId, plugnmeet.RoomArtifactType_SPEECH_TRANSCRIPTION, metadata, false, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create speech transcription file artifact: %w", err)
	}

	return &fileArtifact.ArtifactId, nil
}
