package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

var validUserIDRegex = regexp.MustCompile("^[a-zA-Z0-9-_]+$")

func (m *UserModel) GetPNMJoinToken(ctx context.Context, g *plugnmeet.GenerateTokenReq) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"room_id": g.GetRoomId(),
		"user_id": g.GetUserInfo().GetUserId(),
		"name":    g.GetUserInfo().GetName(),
	})

	// check first
	_ = waitUntilRoomCreationCompletes(ctx, m.rs, g.GetRoomId(), log)

	if g.GetUserInfo().GetName() == config.RecorderUserAuthName {
		return "", errors.New(fmt.Sprintf("name: %s is reserved for internal use only", config.RecorderUserAuthName))
	}

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
		exId := strings.Clone(g.UserInfo.UserId)
		g.UserInfo.UserMetadata.ExUserId = &exId
	}

	if meta.RoomFeatures.AutoGenUserId != nil && *meta.RoomFeatures.AutoGenUserId {
		if g.UserInfo.UserId != config.RecorderBot && g.UserInfo.UserId != config.RtmpBot {
			// we'll auto generate user id no matter what sent
			g.UserInfo.UserId = uuid.NewString()
			log.WithFields(logrus.Fields{
				"ex_user_id":  g.UserInfo.GetUserMetadata().GetExUserId(),
				"new_user_id": g.UserInfo.GetUserId(),
			}).Infof("auto generated user_id: %s", g.UserInfo.GetUserId())
		}
	} else {
		// check if this user is online, then we'll need to log out this user first
		// otherwise will have problems during joining because of duplicate join
		// as from API it was requested to generate a new token, so we won't prevent it
		// and only send log-out signal to the user
		status, err := m.natsService.GetRoomUserStatus(g.GetRoomId(), g.GetUserInfo().GetUserId())
		if err != nil {
			return "", err
		}
		if status == natsservice.UserStatusOnline {
			m.logger.Warnln("same user found in online status, removing that user before re-generating token")

			_ = m.RemoveParticipant(&plugnmeet.RemoveParticipantReq{
				RoomId: g.GetRoomId(),
				UserId: g.GetUserInfo().GetUserId(),
				Msg:    "notifications.room-disconnected-duplicate-entry",
			})

			// Wait for the user to be fully offline before proceeding.
			m.waitForUserToBeOffline(ctx, g.GetRoomId(), g.GetUserInfo().GetUserId())
		}
	}

	// we'll validate user id
	if !validUserIDRegex.MatchString(g.UserInfo.UserId) {
		return "", fmt.Errorf("user_id should only contain ASCII letters (a-z A-Z), digits (0-9) or -_")
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

	am := NewAuthModel(m.app, m.natsService, m.logger.Logger)
	return am.GeneratePNMJoinToken(c)
}

// waitForUserToBeOffline polls until the user's status is no longer "online".
// It includes a timeout to prevent indefinite waiting.
func (m *UserModel) waitForUserToBeOffline(ctx context.Context, roomID, userID string) {
	// We'll wait for a maximum of 5 seconds for the user to be offline.
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond) // Poll every 200ms
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			m.logger.Warnf("timed out waiting for user %s in room %s to go offline", userID, roomID)
			return
		case <-ticker.C:
			status, err := m.natsService.GetRoomUserStatus(roomID, userID)
			if err != nil {
				// An error (e.g., key not found) implies the user is gone.
				m.logger.Infof("user %s in room %s is offline (key not found)", userID, roomID)
				return
			}
			if status != natsservice.UserStatusOnline {
				m.logger.Infof("user %s in room %s is now offline (status: %s)", userID, roomID, status)
				return
			}
			// User is still online, loop will continue.
		}
	}
}
