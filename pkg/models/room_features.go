package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func (m *RoomModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"method": "ChangeVisibility",
	})

	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}
	if roomMeta == nil {
		log.Warnln("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	if err := m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}

func (m *RoomModel) EnableRoomSipDialIn(r *plugnmeet.EnableSipDialInReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"room_id":           r.RoomId,
		"method":            "EnableRoomSipDialIn",
		"hide_phone_number": r.HidePhoneNumber,
	})

	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	if roomMeta == nil {
		log.Warnln("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}

	sipDialInFeatures := roomMeta.RoomFeatures.SipDialInFeatures
	if !sipDialInFeatures.IsAllow {
		log.Warnln("sip dial in feature is not allowed for this room")
		return plugnmeet.StatusCode_PERMISSION_DENIED, config.ErrRoomFeatureNotAllowed
	}

	if sipDialInFeatures.IsActive {
		log.Warnln("sip dial in is already enabled")
		return plugnmeet.StatusCode_CONFLICT, config.ErrRoomSipDialInEnabled
	}
	sipDialInFeatures.HidePhoneNumber = r.HidePhoneNumber

	ruleId, pin, err := m.lk.CreateSIPDispatchRule(r.RoomId, sipDialInFeatures.HidePhoneNumber, log)
	if err != nil {
		log.WithError(err).Errorln("failed to create SIP dispatch rule")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}
	sipDialInFeatures.IsActive = true
	sipDialInFeatures.DispatchRuleId = &ruleId
	sipDialInFeatures.Pin = &pin
	sipDialInFeatures.PhoneNumbers = m.app.LivekitSipInfo.PhoneNumbers

	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}

func (m *RoomModel) CreateIngress(r *plugnmeet.CreateIngressReq) (plugnmeet.StatusCode, *livekit.IngressInfo, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":    r.RoomId,
		"inputType": r.InputType.String(),
		"method":    "CreateIngress",
	})
	log.Infoln("request to create ingress")

	// we'll update room metadata
	metadata, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil, config.ErrRoomUnexpectedError
	}
	if metadata == nil {
		log.Warnln("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, nil, config.ErrRoomNotFound
	}

	ingressFeatures := metadata.RoomFeatures.IngressFeatures
	if !ingressFeatures.IsAllow {
		log.Warnln("ingress feature is not allowed for this room")
		return plugnmeet.StatusCode_PERMISSION_DENIED, nil, config.ErrRoomFeatureNotAllowed
	}
	if ingressFeatures.StreamKey != "" {
		log.Warnln("an ingress is already created for this room")
		return plugnmeet.StatusCode_CONFLICT, nil, config.ErrRoomIngressExists
	}

	inputType := livekit.IngressInput_RTMP_INPUT
	if r.InputType == plugnmeet.IngressInput_WHIP_INPUT {
		inputType = livekit.IngressInput_WHIP_INPUT
	}

	req := &livekit.CreateIngressRequest{
		// RTMP or WHIP
		InputType: inputType,
		// Ingress name
		Name: fmt.Sprintf("%s:%d", r.RoomId, 1),
		// Room to join
		RoomName: r.RoomId,
		// Unique ID for the ingress bot
		ParticipantIdentity: fmt.Sprintf("%s%d", config.IngressUserIdPrefix, time.Now().UnixMilli()),
		// Display name for the bot
		ParticipantName: r.ParticipantName,
	}
	log.WithField("participantIdentity", req.ParticipantIdentity).Info("creating ingress with livekit")

	f, err := m.lk.CreateIngress(req)
	if err != nil {
		log.WithError(err).Errorln("failed to create ingress with livekit")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil, config.ErrRoomUnexpectedError
	}
	if f == nil {
		log.Errorln("livekit returned invalid nil create ingress response")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil, config.ErrRoomUnexpectedError
	}

	// add this user in our bucket
	log.Info("adding ingress participant to NATS user bucket")
	mt := plugnmeet.UserMetadata{
		IsAdmin:         true,
		RecordWebcam:    new(true),
		WaitForApproval: false,
		LockSettings: &plugnmeet.LockSettings{
			LockWebcam:     new(false),
			LockMicrophone: new(false),
		},
	}
	err = m.natsService.AddUser(r.RoomId, req.ParticipantIdentity, r.ParticipantName, true, false, &mt, plugnmeet.ClientType_WEB)
	if err != nil {
		log.WithError(err).Errorln("failed to add ingress user to NATS")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil, config.ErrRoomUnexpectedError
	}

	ingressFeatures.InputType = r.InputType
	ingressFeatures.Url = f.Url
	ingressFeatures.StreamKey = f.StreamKey

	log.Info("updating and broadcasting room metadata with ingress info")
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, metadata)
	if err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil, config.ErrRoomUnexpectedError
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INGRESS_CREATED,
		RoomId:    r.RoomId,
	})

	log.Info("successfully created ingress")
	return plugnmeet.StatusCode_SUCCESS, f, nil
}

// ChangeSharedNotepadStatus toggles the shared notepad active state.
func (m *RoomModel) ChangeSharedNotepadStatus(r *plugnmeet.ChangeSharedNotepadStatusReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   r.RoomId,
		"isActive": r.IsActive,
		"method":   "ChangeEtherpadStatus",
	})
	log.Infoln("Request to change shared notepad status received")

	meta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("Failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}
	if meta == nil {
		log.Warnln("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}

	np := meta.GetRoomFeatures().GetSharedNotePadFeatures()
	if np == nil {
		log.Warnln("shared notepad features not found in room metadata")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}

	if r.IsActive && np.NotePadId == "" {
		np.NotePadId = uuid.NewString()
	}
	np.IsActive = r.IsActive

	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, meta); err != nil {
		log.WithError(err).Errorln("Failed to update and broadcast room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
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
	return plugnmeet.StatusCode_SUCCESS, nil
}

func (m *RoomModel) BroadcastToRoom(r *plugnmeet.BroadcastToRoomReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"method":  "BroadcastToRoom",
		"room_id": r.RoomId,
	})
	toUsersIds := make(map[string]bool)

	if r.OnlyToAdmins {
		users, err := m.natsService.GetOnlineUsersList(r.RoomId)
		if err != nil {
			log.WithError(err).Errorln("failed to get online users list")
			return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
		}

		if users == nil {
			return plugnmeet.StatusCode_USER_NOT_FOUND, config.NoOnlineUserFound
		}

		for _, u := range users {
			if u.IsAdmin {
				toUsersIds[u.UserId] = true
			}
		}
		if len(toUsersIds) == 0 {
			return plugnmeet.StatusCode_USER_NOT_FOUND, config.NoOnlineAdminFound
		}
	}
	if r.ToUserId != nil && *r.ToUserId != "" {
		toUsersIds[*r.ToUserId] = true
	}

	var event plugnmeet.NatsMsgServerToClientEvents
	var data interface{}

	switch v := r.Payload.(type) {
	case *plugnmeet.BroadcastToRoomReq_NotificationMsg:
		event = plugnmeet.NatsMsgServerToClientEvents_SYSTEM_NOTIFICATION
		data = &plugnmeet.NatsSystemNotification{
			Id:        uuid.NewString(),
			Type:      v.NotificationMsg.Type,
			Msg:       v.NotificationMsg.Text,
			SentAt:    time.Now().UnixMilli(),
			WithSound: v.NotificationMsg.WithSound,
		}
	case *plugnmeet.BroadcastToRoomReq_ChatMsg:
		event = plugnmeet.NatsMsgServerToClientEvents_SYSTEM_CHAT_MSG
		data = v.ChatMsg.Message
	}

	if len(toUsersIds) > 0 {
		for u := range toUsersIds {
			if err := m.natsService.BroadcastSystemEventToRoom(event, r.RoomId, data, &u); err != nil {
				log.WithError(err).Errorf("Failed to broadcast message to %s", u)
			}
		}
	} else {
		// to everyone
		if err := m.natsService.BroadcastSystemEventToRoom(event, r.RoomId, data, nil); err != nil {
			log.WithError(err).Error("Failed to broadcast message to the room")
		}
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}
