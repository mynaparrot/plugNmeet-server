package models

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *UserModel) CreateNewPresenter(r *plugnmeet.GenerateTokenReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserInfo.UserId,
		"method": "CreateNewPresenter",
	})
	log.Infoln("request to check for new presenter")

	// first, check if we've any presenter already assigned
	ids, err := m.natsService.GetOnlineUsersId(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get online users")
		return err
	}

	if ids == nil || len(ids) == 0 {
		// no user found
		log.Infoln("no users in room, making this user presenter")
		r.UserInfo.UserMetadata.IsPresenter = true
		return nil
	}

	for _, id := range ids {
		if info, err := m.natsService.GetUserInfo(r.RoomId, id); err == nil && info != nil {
			if info.IsPresenter {
				// we already have a presenter
				log.WithField("presenter_id", id).Infoln("presenter already exists, not making this user presenter")
				r.UserInfo.UserMetadata.IsPresenter = false
				return nil
			}
		} else if err != nil {
			log.WithError(err).WithField("lookup_user_id", id).Warnln("could not get user info while checking for presenter")
		}
	}

	// so, we do not have any presenter
	// we'll make this user as presenter
	log.Infoln("no presenter found, making this user presenter")
	r.UserInfo.UserMetadata.IsPresenter = true
	return nil
}

func (m *UserModel) SwitchPresenter(r *plugnmeet.SwitchPresenterReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":          r.RoomId,
		"userId":          r.UserId,
		"requestedUserId": r.RequestedUserId,
		"task":            r.Task.String(),
		"method":          "SwitchPresenter",
	})
	log.Infoln("request to switch presenter")

	ids := m.natsService.GetUsersIdFromRoomStatusBucket(r.RoomId)
	if ids == nil || len(ids) == 0 {
		err := errors.New("no users found to switch presenter")
		log.WithError(err).Warnln()
		return err
	}

	for _, userId := range ids {
		userLog := log.WithField("iterated_user_id", userId)
		uInfo, metadata, err := m.natsService.GetUserWithMetadata(r.RoomId, userId)
		if err != nil {
			userLog.WithError(err).Errorln("error getting user info")
			continue
		}

		if uInfo == nil {
			continue
		}
		update := false

		if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
			if userId == r.UserId {
				uInfo.IsPresenter = true
				metadata.IsPresenter = uInfo.IsPresenter
				update = true
				userLog.Info("promoting user to presenter")
			} else if uInfo.IsPresenter {
				// demoted this user
				uInfo.IsPresenter = false
				metadata.IsPresenter = uInfo.IsPresenter
				update = true
				userLog.Info("demoting current presenter")
			}
		} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
			// make requested user as presenter first
			// otherwise there won't be any presenter in the room
			if userId == r.RequestedUserId {
				uInfo.IsPresenter = true
				metadata.IsPresenter = uInfo.IsPresenter
				update = true
				userLog.Info("promoting admin to presenter after demoting another")
			} else if uInfo.IsPresenter {
				uInfo.IsPresenter = false
				metadata.IsPresenter = uInfo.IsPresenter
				update = true
				userLog.Info("demoting current presenter")
			}
		}

		if update {
			err = m.natsService.UpdateUserKeyValue(r.RoomId, userId, natsservice.UserIsPresenterKey, fmt.Sprintf("%v", uInfo.IsPresenter))
			if err != nil {
				userLog.WithError(err).Errorln("error updating user is_presenter key")
				continue
			}
			err = m.natsService.UpdateAndBroadcastUserMetadata(r.RoomId, userId, metadata, nil)
			if err != nil {
				userLog.WithError(err).Errorln("error updating and broadcasting user metadata")
			}
		}
	}

	log.Info("presenter switch process completed")
	return nil
}
