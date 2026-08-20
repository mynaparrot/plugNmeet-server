package models

import (
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type SharedNotepadModel struct {
	analyticsModel *AnalyticsModel
	natsService    *natsservice.NatsService
	logger         *logrus.Entry
}

type SharedNotepadModelArgs struct {
	fx.In
	NatsService    *natsservice.NatsService
	AnalyticsModel *AnalyticsModel
	Logger         *logrus.Logger
}

func NewSharedNotepadModel(args SharedNotepadModelArgs) *SharedNotepadModel {
	return &SharedNotepadModel{
		analyticsModel: args.AnalyticsModel,
		natsService:    args.NatsService,
		logger:         args.Logger.WithField("model", "notepad"),
	}
}

// ChangeSharedNotepadStatus toggles the shared notepad active state. On first
// activation it generates the BlockNote document id (note_pad_id). The method
// name is kept for API compatibility with the previous Etherpad notepad.
func (m *SharedNotepadModel) ChangeSharedNotepadStatus(r *plugnmeet.ChangeSharedNotepadStatusReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   r.RoomId,
		"isActive": r.IsActive,
		"method":   "ChangeEtherpadStatus",
	})
	log.Infoln("Request to change shared notepad status received")

	meta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("Failed to get room metadata")
		return err
	}
	if meta == nil {
		log.WithError(config.InvalidNilRoomMetadata).Errorln()
		return config.InvalidNilRoomMetadata
	}

	np := meta.GetRoomFeatures().GetSharedNotePadFeatures()
	if np == nil {
		log.WithError(config.InvalidNilRoomMetadata).Errorln()
		return config.InvalidNilRoomMetadata
	}

	if r.IsActive && np.NotePadId == "" {
		np.NotePadId = uuid.NewString()
	}
	np.IsActive = r.IsActive

	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, meta); err != nil {
		log.WithError(err).Errorln("Failed to update and broadcast room metadata")
		return err
	}

	val := plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String()
	if !r.IsActive {
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	}
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_SHARED_NOTEPAD_STATUS,
		RoomId:    r.RoomId,
		HsetValue: &val,
	})

	log.Info("Successfully changed shared notepad status")
	return nil
}
