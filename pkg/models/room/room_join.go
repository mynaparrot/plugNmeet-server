package roommodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/auth"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *RoomModel) GetPNMJoinToken(g *plugnmeet.GenerateTokenReq) (string, error) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(g.GetRoomId())

	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	if g.UserInfo.IsAdmin {
		g.UserInfo.UserMetadata.IsAdmin = true
		g.UserInfo.UserMetadata.WaitForApproval = false
		// no lock for admin
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)

		err := m.userModel.CreateNewPresenter(g)
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
		m.userModel.AssignLockSettingsToUser(meta, g)

		// if waiting room features active then we won't allow direct access
		if meta.RoomFeatures.WaitingRoomFeatures.IsActive {
			g.UserInfo.UserMetadata.WaitForApproval = true
		}
	}

	if g.UserInfo.UserMetadata.RecordWebcam == nil {
		recordWebcam := true
		g.UserInfo.UserMetadata.RecordWebcam = &recordWebcam
	}

	metadata, err := m.natsService.MarshalUserMetadata(g.UserInfo.UserMetadata)
	if err != nil {
		return "", err
	}

	// update our bucket
	nsts := natsservice.New(m.app)
	err = nsts.AddUser(g.RoomId, g.UserInfo.UserId, "", g.UserInfo.Name, g.UserInfo.IsAdmin, g.UserInfo.UserMetadata.IsPresenter, g.UserInfo.UserMetadata)
	if err != nil {
		return "", err
	}

	// let's update our redis
	_, err = m.rs.ManageRoomWithUsersMetadata(g.RoomId, g.UserInfo.UserId, "add", metadata)
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

	am := authmodel.New(m.app, nil)
	return am.GeneratePNMJoinToken(c)
}
