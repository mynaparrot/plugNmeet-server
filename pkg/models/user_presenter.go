package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

// CreateNewPresenter verify if any presenter already online or not
// if not then it promote requested admin to be presenter
func (m *UserModel) CreateNewPresenter(r *plugnmeet.GenerateTokenReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserInfo.UserId,
		"method": "CreateNewPresenter",
	})
	log.Infoln("request to check for new presenter")

	presenter, err := m.findCurrentPresenter(r.RoomId)
	if err != nil {
		log.WithError(err).Warnln("failed to find current presenter")
		return err
	}
	if presenter != "" {
		log.WithField("presenter", presenter).Info("session already have an online presenter, skipping")
		return nil
	}

	log.Infoln("no presenter found, making this user presenter")
	r.UserInfo.UserMetadata.IsPresenter = true
	return nil
}

// SwitchPresenter promotes the new presenter *before* demoting the old one and ensures
// the requesting admin becomes the presenter on demotion, guaranteeing the
// room is never left without a presenter.
func (m *UserModel) SwitchPresenter(r *plugnmeet.SwitchPresenterReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":          r.RoomId,
		"userId":          r.UserId,
		"requestedUserId": r.RequestedUserId,
		"task":            r.Task.String(),
		"method":          "SwitchPresenter",
	})
	log.Infoln("request to switch presenter")

	var newPresenterId, oldPresenterId string

	if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
		newPresenterId = r.UserId
		// Find the current presenter so we can demote them later.
		currentPresenter, err := m.findCurrentPresenter(r.RoomId)
		if err != nil {
			log.WithError(err).Warnln("could not find current presenter to demote")
		}
		oldPresenterId = currentPresenter

	} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
		oldPresenterId = r.UserId
		// The admin making the request is the safe fallback to prevent a presenter-less room.
		newPresenterId = r.RequestedUserId
	}

	// 1. Promote the new presenter first.
	err := m.updatePresenterStatus(r.RoomId, newPresenterId, true)
	if err != nil {
		// If we can't even promote the new presenter, we must stop.
		log.WithError(err).WithField("promote_user_id", newPresenterId).Errorln("failed to promote new presenter")
		return err
	}

	// 2. Only after a successful promotion, demote the old presenter.
	// Ensure we don't accidentally demote the person we just promoted.
	if oldPresenterId != "" && oldPresenterId != newPresenterId {
		err = m.updatePresenterStatus(r.RoomId, oldPresenterId, false)
		if err != nil {
			// This is not a fatal error. The room now has two presenters,
			// which is better than zero. We should log it as a warning.
			log.WithError(err).WithField("demote_user_id", oldPresenterId).Warnln("successfully promoted new presenter but failed to demote old one")
		}
	}

	log.Info("presenter switch process completed successfully")
	return nil
}

// findCurrentPresenter check all the online users to find the current presenter
func (m *UserModel) findCurrentPresenter(roomId string) (string, error) {
	ids, err := m.natsService.GetOnlineUsersId(roomId)
	if err != nil {
		return "", fmt.Errorf("failed to get online users: %w", err)
	}

	if ids == nil || len(ids) == 0 {
		return "", nil
	}

	for _, userId := range ids {
		if isPresenter := m.natsService.IsUserPresenter(roomId, userId); isPresenter {
			return userId, nil
		}
	}

	return "", nil // No presenter found.
}

// updatePresenterStatus performs the complete, two-step update for a user's presenter status.
func (m *UserModel) updatePresenterStatus(roomId, userId string, isPresenter bool) error {
	// 1. Update the primary Key-Value store first.
	err := m.natsService.UpdateUserKeyValue(roomId, userId, natsservice.UserIsPresenterKey, fmt.Sprintf("%v", isPresenter))
	if err != nil {
		return fmt.Errorf("failed to update user key-value for %s: %w", userId, err)
	}

	// 2. Fetch the current metadata, update it, and then broadcast the change to clients.
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		return fmt.Errorf("failed to get user metadata for %s: %w", userId, err)
	}
	if metadata == nil {
		return fmt.Errorf("no metadata found for user %s", userId)
	}

	metadata.IsPresenter = isPresenter
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		return fmt.Errorf("failed to update and broadcast metadata for %s: %w", userId, err)
	}

	m.logger.Infof("Successfully set is_presenter=%v for user %s and broadcasted change", isPresenter, userId)
	return nil
}
