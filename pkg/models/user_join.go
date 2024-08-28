package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *UserModel) GetPNMJoinToken(g *plugnmeet.GenerateTokenReq) (string, error) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(g.GetRoomId())

	status, err := m.natsService.GetRoomStatus(g.RoomId)
	if err != nil {
		return "", err
	}
	if status == natsservice.RoomStatusEnded {
		return "", errors.New("room found in delete status, need to recreate it")
	}

	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	if g.UserInfo.IsAdmin {
		g.UserInfo.UserMetadata.IsAdmin = true
		g.UserInfo.UserMetadata.WaitForApproval = false
		// no lock for admin
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)

		err := m.CreateNewPresenter(g)
		if err != nil {
			return "", err
		}
	} else {
		meta, err := m.natsService.GetRoomMetadataStruct(g.RoomId)
		if err != nil {
			return "", err
		}
		if meta == nil {
			return "", errors.New("room metadata not found")
		}
		m.AssignLockSettingsToUser(meta, g)

		// if waiting room features active then we won't allow direct access
		if meta.RoomFeatures.WaitingRoomFeatures.IsActive {
			g.UserInfo.UserMetadata.WaitForApproval = true
		}
	}

	if g.UserInfo.UserMetadata.RecordWebcam == nil {
		recordWebcam := true
		g.UserInfo.UserMetadata.RecordWebcam = &recordWebcam
	}

	// add user to our bucket
	err = m.natsService.AddUser(g.RoomId, g.UserInfo.UserId, g.UserInfo.Name, g.UserInfo.IsAdmin, g.UserInfo.UserMetadata.IsPresenter, g.UserInfo.UserMetadata)
	if err != nil {
		return "", err
	}

	c := &plugnmeet.PlugNmeetTokenClaims{
		Name:     g.UserInfo.Name,
		UserId:   g.UserInfo.UserId,
		RoomId:   g.RoomId,
		IsAdmin:  g.UserInfo.IsAdmin,
		IsHidden: g.UserInfo.IsHidden,
	}

	am := NewAuthModel(m.app, nil)
	return am.GeneratePNMJoinToken(c)
}
