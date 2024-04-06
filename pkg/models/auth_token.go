package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type AuthTokenModel struct {
	app *config.AppConfig
	rs  *RoomService
}

func NewAuthTokenModel() *AuthTokenModel {
	return &AuthTokenModel{
		app: config.AppCnf,
		rs:  NewRoomService(),
	}
}

func (a *AuthTokenModel) GeneratePlugNmeetAccessToken(g *plugnmeet.GenerateTokenReq) (string, error) {
	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	a.assignLockSettings(g)
	if g.UserInfo.IsAdmin {
		a.makePresenter(g)
	}

	if g.UserInfo.UserMetadata.RecordWebcam == nil {
		recordWebcam := true
		g.UserInfo.UserMetadata.RecordWebcam = &recordWebcam
	}

	metadata, err := a.rs.MarshalParticipantMetadata(g.UserInfo.UserMetadata)
	if err != nil {
		return "", err
	}

	// let's update our redis
	_, err = a.rs.ManageRoomWithUsersMetadata(g.RoomId, g.UserInfo.UserId, "add", metadata)
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

	return auth.GeneratePlugNmeetJWTAccessToken(a.app.Client.ApiKey, a.app.Client.Secret, g.UserInfo.UserId, a.app.LivekitInfo.TokenValidity, c)
}

func (a *AuthTokenModel) VerifyPlugNmeetAccessToken(token string) (*plugnmeet.PlugNmeetTokenClaims, error) {
	return auth.VerifyPlugNmeetAccessToken(a.app.Client.ApiKey, a.app.Client.Secret, token)
}

func (a *AuthTokenModel) assignLockSettings(g *plugnmeet.GenerateTokenReq) {
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

	_, meta, err := a.rs.LoadRoomWithMetadata(g.RoomId)
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

func (a *AuthTokenModel) makePresenter(g *plugnmeet.GenerateTokenReq) {
	if g.UserInfo.IsAdmin && !g.UserInfo.IsHidden {
		participants, err := a.rs.LoadParticipants(g.RoomId)
		if err != nil {
			return
		}

		hasPresenter := false
		for _, p := range participants {
			meta := make([]byte, len(p.Metadata))
			copy(meta, p.Metadata)

			m, _ := a.rs.UnmarshalParticipantMetadata(string(meta))
			if m.IsAdmin && m.IsPresenter {
				hasPresenter = true
				break
			}
		}

		if !hasPresenter {
			g.UserInfo.UserMetadata.IsPresenter = true
		}
	}
}

type RenewTokenReq struct {
	Token string `json:"token"`
}

// DoRenewPlugNmeetToken we'll renew token
func (a *AuthTokenModel) DoRenewPlugNmeetToken(token string) (string, error) {
	claims, err := a.VerifyPlugNmeetAccessToken(token)
	if err != nil {
		return "", err
	}

	// load current information
	p, err := a.rs.ManageActiveUsersList(claims.RoomId, claims.UserId, "get", 0)
	if err != nil {
		return "", err
	}
	if len(p) == 0 {
		return "", errors.New("user isn't online")
	}

	return auth.GeneratePlugNmeetJWTAccessToken(a.app.Client.ApiKey, a.app.Client.Secret, claims.UserId, a.app.LivekitInfo.TokenValidity, claims)
}

func (a *AuthTokenModel) GenerateLivekitToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	metadata, err := a.rs.ManageRoomWithUsersMetadata(c.RoomId, c.UserId, "get", "")
	if err != nil {
		return "", err
	}
	// without any metadata, we won't continue
	if metadata == "" {
		return "", errors.New("empty user metadata")
	}

	return auth.GenerateLivekitAccessToken(a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret, a.app.LivekitInfo.TokenValidity, c, metadata)
}

func (a *AuthTokenModel) ValidateLivekitWebhookToken(body []byte, token string) (bool, error) {
	return webhook.VerifyRequest(body, a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret, token)
}
