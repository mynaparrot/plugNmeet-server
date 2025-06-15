package models

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
)

func (m *UserModel) CreateNewPresenter(r *plugnmeet.GenerateTokenReq) error {
	// first, check if we've any presenter already assigned
	ids, err := m.natsService.GetOnlineUsersId(r.RoomId)
	if err != nil {
		return err
	}

	if ids == nil || len(ids) == 0 {
		// no user found
		r.UserInfo.UserMetadata.IsPresenter = true
		return nil
	}

	for _, id := range ids {
		if info, err := m.natsService.GetUserInfo(r.RoomId, id); err == nil && info != nil {
			if info.IsPresenter {
				// we already have a presenter
				r.UserInfo.UserMetadata.IsPresenter = false
				return nil
			}
		}
	}

	// so, we do not have any presenter
	// we'll make this user as presenter
	r.UserInfo.UserMetadata.IsPresenter = true
	return nil
}

func (m *UserModel) SwitchPresenter(r *plugnmeet.SwitchPresenterReq) error {
	ids := m.natsService.GetUsersIdFromRoomStatusBucket(r.RoomId)
	if ids == nil || len(ids) == 0 {
		return errors.New("no users found")
	}

	for _, userId := range ids {
		uInfo, metadata, err := m.natsService.GetUserWithMetadata(r.RoomId, userId)
		if err != nil {
			log.Errorln(err)
			continue
		}

		if uInfo == nil {
			continue
		}
		update := false

		if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
			if userId == r.UserId {
				uInfo.IsPresenter = true
				metadata.IsPresenter = true
				update = true
			} else if uInfo.IsPresenter {
				// demoted this user
				uInfo.IsPresenter = false
				metadata.IsPresenter = false
				update = true
			}
		} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
			// make requested user as presenter first
			// otherwise there won't be any presenter in the room
			if userId == r.RequestedUserId {
				uInfo.IsPresenter = true
				metadata.IsPresenter = true
				update = true
			} else if uInfo.IsPresenter {
				uInfo.IsPresenter = false
				metadata.IsPresenter = false
				update = true
			}
		}

		if update {
			err = m.natsService.UpdateUserKeyValue(r.RoomId, userId, natsservice.UserIsPresenterKey, fmt.Sprintf("%v", uInfo.IsPresenter))
			if err != nil {
				log.Errorln(err)
				continue
			}
			err = m.natsService.UpdateAndBroadcastUserMetadata(r.RoomId, userId, metadata, nil)
			if err != nil {
				log.Errorln(err)
			}
		}
	}
	return nil
}
