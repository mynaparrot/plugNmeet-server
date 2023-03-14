package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"time"
)

type IngressModel struct {
	lk  *lksdk.IngressClient
	ctx context.Context
	rs  *RoomService
}

func NewIngressModel() *IngressModel {
	lk := lksdk.NewIngressClient(config.AppCnf.LivekitInfo.Host, config.AppCnf.LivekitInfo.ApiKey, config.AppCnf.LivekitInfo.Secret)

	return &IngressModel{
		lk:  lk,
		ctx: context.Background(),
		rs:  NewRoomService(),
	}
}

func (m *IngressModel) CreateIngress(r *plugnmeet.CreateIngressReq) (*livekit.IngressInfo, error) {
	// we'll update room metadata
	_, metadata, err := m.rs.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return nil, err
	}
	ingressFeatures := metadata.RoomFeatures.IngressFeatures
	if !ingressFeatures.IsAllow {
		return nil, errors.New("ingress feature isn't allow")
	}
	if ingressFeatures.StreamKey != "" {
		return nil, errors.New("multiple ingress creation request not allow")
	}

	req := &livekit.CreateIngressRequest{
		InputType:           livekit.IngressInput_RTMP_INPUT,
		Name:                fmt.Sprintf("%s:%d", r.RoomId, 1),
		RoomName:            r.RoomId,
		ParticipantIdentity: fmt.Sprintf("%d", time.Now().UnixMilli()),
		ParticipantName:     r.ParticipantName,
	}

	f, err := m.lk.CreateIngress(m.ctx, req)
	if err != nil {
		return nil, err
	}

	ingressFeatures.Url = f.Url
	ingressFeatures.StreamKey = f.StreamKey

	_, err = m.rs.UpdateRoomMetadataByStruct(r.RoomId, metadata)
	if err != nil {
		return nil, err
	}

	return f, err
}
