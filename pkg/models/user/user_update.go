package usermodel

import (
	"errors"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/datamsg"
)

func (u *UserModel) RemoveParticipant(r *plugnmeet.RemoveParticipantReq) error {
	p, err := u.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State != livekit.ParticipantInfo_ACTIVE {
		return errors.New(config.UserNotActive)
	}

	// send message to user first
	dm := datamsgmodel.New(u.app, u.ds, u.rs, u.lk)
	_ = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: plugnmeet.DataMsgBodyType_ALERT,
		Msg:         r.Msg,
		RoomId:      r.RoomId,
		SendTo:      []string{p.Identity},
	})

	// now remove
	_, err = u.lk.RemoveParticipant(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	// finally check if requested to block as well as
	if r.BlockUser {
		_, _ = u.rs.AddUserToBlockList(r.RoomId, r.UserId)
	}

	return nil
}

func (u *UserModel) SwitchPresenter(r *plugnmeet.SwitchPresenterReq) error {
	participants, err := u.lk.LoadParticipants(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		meta := make([]byte, len(p.Metadata))
		copy(meta, p.Metadata)

		m, _ := u.natsService.UnmarshalUserMetadata(string(meta))

		if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
			if m.IsPresenter {
				// demote current presenter from presenter
				m.IsPresenter = false
				err = u.natsService.UpdateAndBroadcastUserMetadata(r.RoomId, p.Identity, m, nil)
				if err != nil {
					return errors.New(config.CanNotDemotePresenter)
				}
			}
		} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
			if p.Identity == r.RequestedUserId {
				// we'll update requested user as presenter
				// otherwise in the session there won't have any presenter
				m.IsPresenter = true
				err = u.natsService.UpdateAndBroadcastUserMetadata(r.RoomId, p.Identity, m, nil)
				if err != nil {
					return errors.New(config.CanNotChangeAlternativePresenter)
				}
			}
		}
	}

	// if everything goes well in top then we'll go ahead
	p, err := u.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(p.Metadata))
	copy(meta, p.Metadata)

	m, _ := u.natsService.UnmarshalUserMetadata(string(meta))

	if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
		m.IsPresenter = true
		err = u.natsService.UpdateAndBroadcastUserMetadata(r.RoomId, p.Identity, m, nil)
		if err != nil {
			return errors.New(config.CanNotPromoteToPresenter)
		}
	} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
		m.IsPresenter = false
		err = u.natsService.UpdateAndBroadcastUserMetadata(r.RoomId, p.Identity, m, nil)
		if err != nil {
			return errors.New(config.CanNotDemotePresenter)
		}
	}

	return nil
}
