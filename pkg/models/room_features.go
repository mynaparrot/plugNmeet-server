package models

import (
	"fmt"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func (m *RoomModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (bool, string) {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return false, err.Error()
	}
	if roomMeta == nil {
		return false, "invalid nil room metadata information"
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}

func (m *RoomModel) EnableRoomSipDialIn(r *plugnmeet.EnableSipDialInReq) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}

	if roomMeta == nil {
		return fmt.Errorf("invalid nil room metadata information")
	}

	sipDialInFeatures := roomMeta.RoomFeatures.SipDialInFeatures
	if !sipDialInFeatures.IsAllow {
		return fmt.Errorf("sip dial feature isn't allow for this room")
	}

	if sipDialInFeatures.IsActive {
		return fmt.Errorf("sip dial in is already enabled")
	}
	log := m.logger.WithFields(logrus.Fields{
		"room_id":           r.RoomId,
		"method":            "EnableRoomSipDialIn",
		"hide_phone_number": r.HidePhoneNumber,
	})
	sipDialInFeatures.HidePhoneNumber = r.HidePhoneNumber

	ruleId, pin, err := m.lk.CreateSIPDispatchRule(r.RoomId, sipDialInFeatures.HidePhoneNumber, log)
	if err != nil {
		log.WithError(err).Errorln("failed to create SIP dispatch rule")
		return fmt.Errorf("failed to create SIP dispatch rule")
	}
	sipDialInFeatures.IsActive = true
	sipDialInFeatures.DispatchRuleId = &ruleId
	sipDialInFeatures.Pin = &pin
	sipDialInFeatures.PhoneNumbers = m.app.LivekitSipInfo.PhoneNumbers

	return m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)
}

func (m *RoomModel) CreateIngress(r *plugnmeet.CreateIngressReq) (*livekit.IngressInfo, error) {
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
		return nil, err
	}
	if metadata == nil {
		err = fmt.Errorf("invalid nil room metadata information")
		log.WithError(err).Errorln()
		return nil, err
	}

	ingressFeatures := metadata.RoomFeatures.IngressFeatures
	if !ingressFeatures.IsAllow {
		err = fmt.Errorf("ingress feature isn't allowed for this room")
		log.WithError(err).Warnln()
		return nil, err
	}
	if ingressFeatures.StreamKey != "" {
		err = fmt.Errorf("multiple ingress creation request not allowed")
		log.WithError(err).Warnln()
		return nil, err
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
		return nil, err
	}
	if f == nil {
		err = fmt.Errorf("livekit returned invalid nil create ingress response")
		log.WithError(err).Errorln()
		return nil, err
	}

	// add this user in our bucket
	log.Info("adding ingress participant to NATS user bucket")
	mt := plugnmeet.UserMetadata{
		IsAdmin:         true,
		RecordWebcam:    proto.Bool(true),
		WaitForApproval: false,
		LockSettings: &plugnmeet.LockSettings{
			LockWebcam:     proto.Bool(false),
			LockMicrophone: proto.Bool(false),
		},
	}
	err = m.natsService.AddUser(r.RoomId, req.ParticipantIdentity, r.ParticipantName, true, false, &mt)
	if err != nil {
		log.WithError(err).Errorln("failed to add ingress user to NATS")
		return nil, err
	}

	ingressFeatures.InputType = r.InputType
	ingressFeatures.Url = f.Url
	ingressFeatures.StreamKey = f.StreamKey

	log.Info("updating and broadcasting room metadata with ingress info")
	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, metadata)
	if err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
		return nil, err
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INGRESS_CREATED,
		RoomId:    r.RoomId,
	})

	log.Info("successfully created ingress")
	return f, err
}
