package models

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
)

type ArtifactEventName string

const (
	ArtifactCreated ArtifactEventName = "artifact_created"
)

type ArtifactModel struct {
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	webhookNotifier *helpers.WebhookNotifier
	log             *logrus.Entry
}

func NewArtifactModel(app *config.AppConfig, ds *dbservice.DatabaseService, webhookNotifier *helpers.WebhookNotifier) *ArtifactModel {
	return &ArtifactModel{
		app:             app,
		ds:              ds,
		webhookNotifier: webhookNotifier,
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

	m.log.Infof("moved artifact file %s to trash at %s", filePath, trashPath)
	return trashPath, nil
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
				Type:       artifact.Type,
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
