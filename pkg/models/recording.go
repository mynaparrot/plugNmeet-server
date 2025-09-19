package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

type RecordingModel struct {
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	lk              *livekitservice.LivekitService
	analyticsModel  *AnalyticsModel
	webhookNotifier *helpers.WebhookNotifier
	natsService     *natsservice.NatsService
	logger          *logrus.Entry
}

func NewRecordingModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, analyticsModel *AnalyticsModel, webhookNotifier *helpers.WebhookNotifier, logger *logrus.Logger) *RecordingModel {
	return &RecordingModel{
		app:             app,
		ds:              ds,
		rs:              rs,
		analyticsModel:  analyticsModel,
		webhookNotifier: webhookNotifier,
		natsService:     natsService,
		logger:          logger.WithField("model", "recording"),
	}
}

func (m *RecordingModel) HandleRecorderResp(r *plugnmeet.RecorderToPlugNmeet, roomInfo *dbmodels.RoomInfo) {
	switch r.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		m.recordingStarted(r)
		go m.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_END_RECORDING:
		m.recordingEnded(r)
		go m.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_START_RTMP:
		m.rtmpStarted(r)
		go m.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_END_RTMP:
		m.rtmpEnded(r)
		go m.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_RECORDING_PROCEEDED:
		creation, err := m.addRecordingInfoToDB(r, roomInfo.CreationTime)
		if err != nil {
			m.logger.WithError(err).Errorln("error adding recording info to db")
		}
		// keep record of this file
		m.addRecordingInfoFile(r, creation, roomInfo)
		go m.sendToWebhookNotifier(r)
	}
}

func (m *RecordingModel) sendToWebhookNotifier(r *plugnmeet.RecorderToPlugNmeet) {
	tk := r.Task.String()
	n := m.webhookNotifier
	if n != nil {
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &tk,
			Room: &plugnmeet.NotifyEventRoom{
				Sid:    &r.RoomSid,
				RoomId: &r.RoomId,
			},
			RecordingInfo: &plugnmeet.RecordingInfoEvent{
				RecordId:    r.RecordingId,
				RecorderId:  r.RecorderId,
				RecorderMsg: r.Msg,
				FilePath:    &r.FilePath,
				FileSize:    &r.FileSize,
			},
		}
		if r.Task == plugnmeet.RecordingTasks_RECORDING_PROCEEDED {
			// this process may take longer time & webhook url may clean up
			// so, here we'll use ForceToPutInQueue method to retrieve url from mysql table
			n.ForceToPutInQueue(msg)
		} else {
			err := n.SendWebhookEvent(msg)
			if err != nil {
				m.logger.WithError(err).Errorln("error sending webhook event")
			}
		}
	}

	// send analytics
	var val string
	data := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		RoomId:    r.RoomId,
	}

	switch r.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RECORDING_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String() + ":" + r.RecorderId
	case plugnmeet.RecordingTasks_END_RECORDING:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RECORDING_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	case plugnmeet.RecordingTasks_START_RTMP:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RTMP_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String() + ":" + r.RecorderId
	case plugnmeet.RecordingTasks_END_RTMP:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RTMP_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	}
	data.HsetValue = &val
	m.analyticsModel.HandleEvent(data)
}
