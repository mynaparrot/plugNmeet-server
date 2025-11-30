package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
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

func (m *ArtifactModel) sendWebhookNotification(eventName, roomSid string, artifact *dbmodels.RoomArtifact, tokenUsage *plugnmeet.RoomArtifactTokenUsage) {
	if m.webhookNotifier != nil {
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &eventName,
			Room: &plugnmeet.NotifyEventRoom{
				Sid:    &roomSid,
				RoomId: &artifact.RoomId,
			},
			RoomArtifact: &plugnmeet.RoomArtifactWebhookEvent{
				Type:       artifact.Type,
				ArtifactId: artifact.ArtifactId,
				Metadata:   &artifact.Metadata,
				TokenUsage: tokenUsage,
			},
		}

		err := m.webhookNotifier.SendWebhookEvent(msg)
		if err != nil {
			m.log.WithError(err).Errorln("error sending room created webhook")
		}
	}
}
