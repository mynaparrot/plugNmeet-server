package models

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

type ArtifactEventName string

const (
	ArtifactCreated ArtifactEventName = "artifact_created"
)

type ArtifactModel struct {
	ctx             context.Context
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	natsService     *natsservice.NatsService
	webhookNotifier *helpers.WebhookNotifier
	analyticsModel  *AnalyticsModel
	log             *logrus.Entry
}

func NewArtifactModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, redisService *redisservice.RedisService, natsService *natsservice.NatsService, webhookNotifier *helpers.WebhookNotifier, analyticsModel *AnalyticsModel) *ArtifactModel {
	return &ArtifactModel{
		ctx:             ctx,
		app:             app,
		ds:              ds,
		rs:              redisService,
		natsService:     natsService,
		webhookNotifier: webhookNotifier,
		analyticsModel:  analyticsModel,
		log:             app.Logger.WithField("model", "artifact"),
	}
}

func (m *ArtifactModel) buildPath(fileName, roomId string, artifactType plugnmeet.RoomArtifactType) (relativePath string, absolutePath string, err error) {
	relativeDir := filepath.Join(strings.ToLower(artifactType.String()), roomId)
	absoluteDir := filepath.Join(*m.app.ArtifactsSettings.StoragePath, relativeDir)

	err = os.MkdirAll(absoluteDir, 0755)
	if err != nil {
		return "", "", fmt.Errorf("failed to create artifact directory: %w", err)
	}

	relativePath = filepath.Join(relativeDir, fileName)
	absolutePath = filepath.Join(absoluteDir, fileName)
	return
}

// MoveToTrash moves a specified file to the configured backup/trash directory.
// It returns the new path of the file in the trash directory.
func (m *ArtifactModel) MoveToTrash(filePath string) (string, error) {
	if !m.app.ArtifactsSettings.EnableDelArtifactsBackup {
		// If backup is disabled, delete the file permanently.
		err := os.Remove(filePath)
		if err != nil {
			return "", err
		}
		return "", nil // Return empty string to indicate permanent deletion
	}

	// Construct the destination path in the trash directory
	fileName := filepath.Base(filePath)
	trashPath := filepath.Join(m.app.ArtifactsSettings.DelArtifactsBackupPath, fileName)

	// Use os.Rename to move the file.
	err := os.Rename(filePath, trashPath)
	if err != nil {
		return "", err
	}

	// Update the modification time otherwise janitor will delete it based on old value.
	currentTime := time.Now().UTC()
	err = os.Chtimes(trashPath, currentTime, currentTime)
	if err != nil {
		// Log a warning and continue.
		m.log.WithError(err).Warnf("failed to update modification time for moved artifact: %s", trashPath)
	}

	m.log.Infof("moved artifact file %s to trash at %s", filePath, trashPath)
	return trashPath, nil
}

// CreateAllRoomUsageArtifacts is responsible for creating artifact records for all usage-based services
// when a room ends.
func (m *ArtifactModel) CreateAllRoomUsageArtifacts(roomId, roomSid string, roomTableId uint64, log *logrus.Entry) {
	log = log.WithFields(logrus.Fields{
		"method": "CreateAllRoomUsageArtifacts",
	})

	// Speech Transcription file
	transFileArtifactId, err := m.createSpeechTranscriptionFileArtifact(roomId, roomSid, roomTableId, log)
	if err != nil {
		log.WithError(err).Error("failed to create speech transcription artifact")
	}

	// Speech Transcription
	if err := m.createSpeechTranscriptionUsageArtifact(roomId, roomSid, roomTableId, transFileArtifactId, log); err != nil {
		log.WithError(err).Error("failed to create speech transcription usage artifact")
	}

	// Chat Translation
	if err := m.createChatTranslationUsageArtifact(roomId, roomSid, roomTableId, log); err != nil {
		log.WithError(err).Error("failed to create chat translation usage artifact")
	}

	// Synthesized Speech
	if err := m.createSynthesizedSpeechUsageArtifact(roomId, roomSid, roomTableId, log); err != nil {
		log.WithError(err).Error("failed to create synthesized speech usage artifact")
	}

	// AI Text Chat Usage (chat + summary)
	if err := m.createAITextChatUsageArtifacts(roomId, roomSid, roomTableId, log); err != nil {
		log.WithError(err).Error("failed to create AI text chat session usage artifact")
	}
}

func (m *ArtifactModel) sendWebhookNotification(eventName ArtifactEventName, roomSid string, artifact *dbmodels.RoomArtifact, metadata *plugnmeet.RoomArtifactMetadata) {
	if m.webhookNotifier != nil {
		e := string(eventName)
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &e,
			Room: &plugnmeet.NotifyEventRoom{
				Sid:    &roomSid,
				RoomId: &artifact.RoomId,
			},
			RoomArtifact: &plugnmeet.RoomArtifactWebhookEvent{
				Type:       plugnmeet.RoomArtifactType(artifact.Type),
				ArtifactId: artifact.ArtifactId,
				Metadata:   metadata,
			},
		}

		err := m.webhookNotifier.SendWebhookEvent(msg)
		if err != nil {
			m.log.WithError(err).Errorln("error sending room created webhook")
		}
	}
}

func (m *ArtifactModel) HandleAnalyticsEvent(roomId string, eventName plugnmeet.AnalyticsEvents, hSetValue *string, eventValueInteger *int64) {
	d := &plugnmeet.AnalyticsDataMsg{
		EventType:         plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName:         eventName,
		RoomId:            roomId,
		HsetValue:         hSetValue,
		EventValueInteger: eventValueInteger,
	}

	m.analyticsModel.HandleEvent(d)
}

// createAndSaveArtifact is a helper to save data to DB.
func (m *ArtifactModel) createAndSaveArtifact(roomId, roomSid string, roomTableId uint64, artifactType plugnmeet.RoomArtifactType, metadata *plugnmeet.RoomArtifactMetadata, log *logrus.Entry) (*dbmodels.RoomArtifact, error) {
	metadataBytes, err := protojson.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	artifact := &dbmodels.RoomArtifact{
		ArtifactId:  uuid.NewString(),
		RoomTableID: roomTableId,
		RoomId:      roomId,
		Type:        dbmodels.RoomArtifactType(artifactType),
		Metadata:    string(metadataBytes),
	}

	_, err = m.ds.CreateRoomArtifact(artifact)
	if err != nil {
		return nil, fmt.Errorf("failed to create room artifact record: %w", err)
	}

	m.sendWebhookNotification(ArtifactCreated, roomSid, artifact, metadata)
	log.Infof("successfully created %s artifact (id: %s) for room %s", artifactType.String(), artifact.ArtifactId, roomId)
	return artifact, nil
}

func roundAndPointer(val float64, precision int) *float64 {
	multiplier := math.Pow10(precision)
	rounded := math.Round(val*multiplier) / multiplier
	return &rounded
}
