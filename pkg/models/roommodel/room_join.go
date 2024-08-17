package roommodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/authmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/natsservice"
)

func (m *RoomModel) GetPNMJoinToken(g *plugnmeet.GenerateTokenReq) (string, error) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(g.GetRoomId())

	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	m.assignLockSettings(g)
	if g.UserInfo.IsAdmin {
		m.makePresenter(g)
	}

	if g.UserInfo.UserMetadata.RecordWebcam == nil {
		recordWebcam := true
		g.UserInfo.UserMetadata.RecordWebcam = &recordWebcam
	}

	metadata, err := m.lk.MarshalParticipantMetadata(g.UserInfo.UserMetadata)
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

func (m *RoomModel) assignLockSettings(g *plugnmeet.GenerateTokenReq) {
	if g.UserInfo.UserMetadata.LockSettings == nil {
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)
	}
	l := new(plugnmeet.LockSettings)
	ul := g.UserInfo.UserMetadata.LockSettings

	if g.UserInfo.IsAdmin {
		// we'll keep this for future usage
		g.UserInfo.UserMetadata.IsAdmin = true
		g.UserInfo.UserMetadata.WaitForApproval = false
		lock := new(bool)

		// for admin user don't need to service anything
		l.LockMicrophone = lock
		l.LockWebcam = lock
		l.LockScreenSharing = lock
		l.LockChat = lock
		l.LockChatSendMessage = lock
		l.LockChatFileShare = lock
		l.LockWhiteboard = lock
		l.LockSharedNotepad = lock
		l.LockPrivateChat = lock

		g.UserInfo.UserMetadata.LockSettings = l
		return
	}

	_, meta, err := m.lk.LoadRoomWithMetadata(g.RoomId)
	if err != nil {
		g.UserInfo.UserMetadata.LockSettings = l
	}

	// if no lock settings were for this user
	// then we'll use default room lock settings
	dl := meta.DefaultLockSettings

	if ul.LockWebcam == nil && dl.LockWebcam != nil {
		l.LockWebcam = dl.LockWebcam
	} else if ul.LockWebcam != nil {
		l.LockWebcam = ul.LockWebcam
	}

	if ul.LockMicrophone == nil && dl.LockMicrophone != nil {
		l.LockMicrophone = dl.LockMicrophone
	} else if ul.LockMicrophone != nil {
		l.LockMicrophone = ul.LockMicrophone
	}

	if ul.LockScreenSharing == nil && dl.LockScreenSharing != nil {
		l.LockScreenSharing = dl.LockScreenSharing
	} else if ul.LockScreenSharing != nil {
		l.LockScreenSharing = ul.LockScreenSharing
	}

	if ul.LockChat == nil && dl.LockChat != nil {
		l.LockChat = dl.LockChat
	} else if ul.LockChat != nil {
		l.LockChat = ul.LockChat
	}

	if ul.LockChatSendMessage == nil && dl.LockChatSendMessage != nil {
		l.LockChatSendMessage = dl.LockChatSendMessage
	} else if ul.LockChatSendMessage != nil {
		l.LockChatSendMessage = ul.LockChatSendMessage
	}

	if ul.LockChatFileShare == nil && dl.LockChatFileShare != nil {
		l.LockChatFileShare = dl.LockChatFileShare
	} else if ul.LockChatFileShare != nil {
		l.LockChatFileShare = ul.LockChatFileShare
	}

	if ul.LockPrivateChat == nil && dl.LockPrivateChat != nil {
		l.LockPrivateChat = dl.LockPrivateChat
	} else if ul.LockPrivateChat != nil {
		l.LockPrivateChat = ul.LockPrivateChat
	}

	if ul.LockWhiteboard == nil && dl.LockWhiteboard != nil {
		l.LockWhiteboard = dl.LockWhiteboard
	} else if ul.LockWhiteboard != nil {
		l.LockWhiteboard = ul.LockWhiteboard
	}

	if ul.LockSharedNotepad == nil && dl.LockSharedNotepad != nil {
		l.LockSharedNotepad = dl.LockSharedNotepad
	} else if ul.LockSharedNotepad != nil {
		l.LockSharedNotepad = ul.LockSharedNotepad
	}

	// if waiting room feature active then we won't allow direct access
	if meta.RoomFeatures.WaitingRoomFeatures.IsActive {
		g.UserInfo.UserMetadata.WaitForApproval = true
	}

	g.UserInfo.UserMetadata.LockSettings = l
}

func (m *RoomModel) makePresenter(g *plugnmeet.GenerateTokenReq) {
	if g.UserInfo.IsAdmin && !g.UserInfo.IsHidden {
		participants, err := m.lk.LoadParticipants(g.RoomId)
		if err != nil {
			return
		}

		hasPresenter := false
		for _, p := range participants {
			meta := make([]byte, len(p.Metadata))
			copy(meta, p.Metadata)

			mm, _ := m.lk.UnmarshalParticipantMetadata(string(meta))
			if mm.IsAdmin && mm.IsPresenter {
				hasPresenter = true
				break
			}
		}

		if !hasPresenter {
			g.UserInfo.UserMetadata.IsPresenter = true
		}
	}
}
