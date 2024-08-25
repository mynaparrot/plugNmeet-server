package recordingmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analytics"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
)

type RecordingModel struct {
	app             *config.AppConfig
	ds              *dbservice.DatabaseService
	rs              *redisservice.RedisService
	lk              *livekitservice.LivekitService
	analyticsModel  *analyticsmodel.AnalyticsModel
	webhookNotifier *helpers.WebhookNotifier
	natsService     *natsservice.NatsService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *RecordingModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &RecordingModel{
		app:             app,
		ds:              ds,
		rs:              rs,
		analyticsModel:  analyticsmodel.New(app, ds, rs),
		webhookNotifier: helpers.GetWebhookNotifier(app, ds, rs),
		natsService:     natsservice.New(app),
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
			log.Errorln(err)
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
				log.Errorln(err)
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
