package models

import (
	"context"
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
		"room_id":  g.GetRoomId(),
		"user_id":  g.GetUserInfo().GetUserId(),
		"name":     g.GetUserInfo().GetName(),
		"is_admin": g.GetUserInfo().GetIsAdmin(),
		"method":   "GetPNMJoinToken",
	})
	log.Infoln("request to generate join token")

	// Step 1: Wait until any ongoing room creation process is complete to avoid race conditions.
	_ = waitUntilRoomCreationCompletes(ctx, m.rs, g.GetRoomId(), log)

	// Step 2: Validate the user's name to prevent conflicts with reserved system names.
	if g.GetUserInfo().GetName() == config.RecorderUserAuthName {
		err := fmt.Errorf("name: %s is reserved for internal use only", config.RecorderUserAuthName)
		log.WithError(err).Warnln()
		return "", err
	}

	// Step 3: Fetch the current room information and metadata from NATS.
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(g.GetRoomId())
	if err != nil {
		log.WithError(err).Errorln("failed to get room info with metadata")
		return "", err
	}

	if rInfo == nil || meta == nil {
		err = fmt.Errorf("did not find correct room info")
		log.WithError(err).Errorln()
		return "", err
	}

	// Step 4: Ensure the room is not in an ended state.
	if rInfo.Status == natsservice.RoomStatusEnded {
		err = fmt.Errorf("room found in delete status, need to recreate it")
		log.WithError(err).Warnln()
		return "", err
	}

	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	// Step 5: If no external user ID is provided, use the internal user ID as the default.
	if g.UserInfo.UserMetadata.ExUserId == nil || *g.UserInfo.UserMetadata.ExUserId == "" {
		// if empty, then we'll use the default user id
		exId := strings.Clone(g.UserInfo.UserId)
		g.UserInfo.UserMetadata.ExUserId = &exId
	}

	// Step 6: Handle user ID generation and duplicate user checks.
	if meta.RoomFeatures.AutoGenUserId != nil && *meta.RoomFeatures.AutoGenUserId {
		if g.UserInfo.UserId != config.RecorderBot && g.UserInfo.UserId != config.RtmpBot {
			// we'll auto generate user id no matter what sent
			g.UserInfo.UserId = uuid.NewString()
			log.WithFields(logrus.Fields{
				"ex_user_id":  g.UserInfo.GetUserMetadata().GetExUserId(),
				"new_user_id": g.UserInfo.GetUserId(),
			}).Infof("room has auto generated userId feature enabled, assigning new random user_id: %s", g.UserInfo.GetUserId())
		}
	} else {
		// If auto-generation is off, check if a user with the same ID is already online.
		// If so, remove the existing participant to prevent a duplicate join issue.
		status, err := m.natsService.GetRoomUserStatus(g.GetRoomId(), g.GetUserInfo().GetUserId())
		if err != nil {
			log.WithError(err).Errorln("failed to get room user status")
			return "", err
		}
		if status == natsservice.UserStatusOnline {
			log.Warnln("same user found in online status, removing that user before re-generating token")

			_ = m.RemoveParticipant(&plugnmeet.RemoveParticipantReq{
				RoomId: g.GetRoomId(),
				UserId: g.GetUserInfo().GetUserId(),
				Msg:    "notifications.room-disconnected-duplicate-entry",
			})

			// Wait for the user to be fully offline before proceeding.
			m.waitForUserToBeOffline(ctx, g.GetRoomId(), g.GetUserInfo().GetUserId(), log)
		}
	}

	// Step 7: Validate the format of the final user ID.
	if !validUserIDRegex.MatchString(g.UserInfo.UserId) {
		err = fmt.Errorf("user_id should only contain ASCII letters (a-z A-Z), digits (0-9) or -_")
		log.WithError(err).Errorln()
		return "", err
	}

	// Step 8: Assign permissions and lock settings based on whether the user is an admin.
	if g.UserInfo.IsAdmin {
		g.UserInfo.UserMetadata.IsAdmin = true
		g.UserInfo.UserMetadata.WaitForApproval = false
		// no lock for admin
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)

		if err := m.CreateNewPresenter(g); err != nil {
			log.WithError(err).Errorln("failed to create new presenter")
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

	// Step 9: Add the user's information to the NATS key-value store for the room.
	err = m.natsService.AddUser(g.RoomId, g.UserInfo.UserId, g.UserInfo.Name, g.UserInfo.IsAdmin, g.UserInfo.UserMetadata.IsPresenter, g.UserInfo.UserMetadata)
	if err != nil {
		log.WithError(err).Errorln("failed to add user to nats")
		return "", err
	}

	// Step 10: Generate and return the final JWT for the client to use.
	c := &plugnmeet.PlugNmeetTokenClaims{
		Name:     g.UserInfo.Name,
		UserId:   g.UserInfo.UserId,
		RoomId:   g.RoomId,
		IsAdmin:  g.UserInfo.IsAdmin,
		IsHidden: g.UserInfo.IsHidden,
	}

	log.Infoln("successfully generated pnm join token")
	am := NewAuthModel(m.app, m.natsService, m.logger.Logger)
	return am.GeneratePNMJoinToken(c)
}

// waitForUserToBeOffline polls until the user's status is no longer "online".
// It includes a timeout to prevent indefinite waiting.
func (m *UserModel) waitForUserToBeOffline(ctx context.Context, roomID, userID string, log *logrus.Entry) {
	// We'll wait for a maximum of 5 seconds for the user to be offline.
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond) // Poll every 200ms
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			log.Warn("timed out waiting for user to go offline")
			return
		case <-ticker.C:
			status, err := m.natsService.GetRoomUserStatus(roomID, userID)
			if err != nil {
				// An error (e.g., key not found) implies the user is gone.
				log.Info("user is offline (key not found)")
				return
			}
			if status != natsservice.UserStatusOnline {
				log.WithField("status", status).Info("user is now offline")
				return
			}
			// User is still online, loop will continue.
		}
	}
}
