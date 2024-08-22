package usermodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *UserModel) CreateNewPresenter(r *plugnmeet.GenerateTokenReq) error {
	// first, check if we've any presenter already assigned
	ids, err := m.natsService.GetRoomAllUsersFromStatusBucket(r.RoomId)
	if err != nil {
		return err
	}
	if ids == nil || len(ids) == 0 {
		// no user found
		return nil
	}

	for _, id := range ids {
		if entry, err := m.natsService.GetUserKeyValue(r.RoomId, string(id.Value()), natsservice.UserIsPresenterKey); err == nil && entry != nil {
			if string(entry.Value()) == "true" {
				// session already has presenter
				return nil
			}
		}
	}

	// so, we do not have any presenter
	// we'll make this user as presenter
	r.UserInfo.UserMetadata.IsPresenter = true
	return nil
}
