package models

import (
	"fmt"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func (m *IngressModel) CreateIngress(r *plugnmeet.CreateIngressReq) (*livekit.IngressInfo, error) {
	// we'll update room metadata
	metadata, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, fmt.Errorf("invalid nil room metadata information")
	}

	ingressFeatures := metadata.RoomFeatures.IngressFeatures
	if !ingressFeatures.IsAllow {
		return nil, fmt.Errorf("ingress feature isn't allow")
	}
	if ingressFeatures.StreamKey != "" {
		return nil, fmt.Errorf("multiple ingress creation request not allow")
	}

	inputType := livekit.IngressInput_RTMP_INPUT
	if r.InputType == plugnmeet.IngressInput_WHIP_INPUT {
		inputType = livekit.IngressInput_WHIP_INPUT
	}

	req := &livekit.CreateIngressRequest{
		InputType:           inputType,
		Name:                fmt.Sprintf("%s:%d", r.RoomId, 1),
		RoomName:            r.RoomId,
		ParticipantIdentity: fmt.Sprintf("%s%d", config.IngressUserIdPrefix, time.Now().UnixMilli()),
		ParticipantName:     r.ParticipantName,
	}

	f, err := m.lk.CreateIngress(req)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("invalid nil create ingress response")
	}

	// add this user in our bucket
	tr := true
	fl := false
	mt := plugnmeet.UserMetadata{
		IsAdmin:         true,
		RecordWebcam:    &tr,
		WaitForApproval: false,
		LockSettings: &plugnmeet.LockSettings{
			LockWebcam:     &fl,
			LockMicrophone: &fl,
		},
	}
	err = m.natsService.AddUser(r.RoomId, req.ParticipantIdentity, r.ParticipantName, true, false, &mt)
	if err != nil {
		return nil, err
	}

	ingressFeatures.InputType = r.InputType
	ingressFeatures.Url = f.Url
	ingressFeatures.StreamKey = f.StreamKey

	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, metadata)
	if err != nil {
		return nil, err
	}

	// send analytics
	analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs, m.logger.Logger)
	analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_INGRESS_CREATED,
		RoomId:    r.RoomId,
	})

	return f, err
}
