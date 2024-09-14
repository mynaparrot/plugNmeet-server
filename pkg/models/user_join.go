package models

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"regexp"
	"time"
)

func (m *UserModel) GetPNMJoinToken(g *plugnmeet.GenerateTokenReq) (string, error) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(g.GetRoomId())

	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(g.GetRoomId())
	if err != nil {
		return "", err
	}

	if rInfo == nil || meta == nil {
		return "", errors.New("did not find correct room info")
	}

	if rInfo.Status == natsservice.RoomStatusEnded {
		return "", errors.New("room found in delete status, need to recreate it")
	}

	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	if g.UserInfo.UserMetadata.ExUserId == nil || *g.UserInfo.UserMetadata.ExUserId == "" {
		// if empty, then we'll use the default user id
		g.UserInfo.UserMetadata.ExUserId = &g.UserInfo.UserId
	}

	if meta.RoomFeatures.AutoGenUserId != nil && *meta.RoomFeatures.AutoGenUserId {
		if g.UserInfo.UserId != config.RecorderBot && g.UserInfo.UserId != config.RtmpBot {
			// we'll auto generate user id no matter what sent
			g.UserInfo.UserId = fmt.Sprintf("%d", time.Now().UnixMicro())
			log.Infoln("setting up auto generated user_id:", g.UserInfo.UserId, "for name:", g.UserInfo.Name)
		}
	}

	// we'll validate user id
	valid, _ := regexp.MatchString("^[a-zA-Z0-9-_]+$", g.UserInfo.UserId)
	if !valid {
		return "", errors.New("user_id should only contain letters, digits or -_")
	}

	if g.UserInfo.IsAdmin {
		g.UserInfo.UserMetadata.IsAdmin = true
		g.UserInfo.UserMetadata.WaitForApproval = false
		// no lock for admin
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)

		if err := m.CreateNewPresenter(g); err != nil {
			return "", err
		}
	} else {
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

	am := NewAuthModel(m.app, m.natsService)
	return am.GeneratePNMJoinToken(c)
}
