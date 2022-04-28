package models

import (
	"encoding/json"
	"errors"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugNmeet/internal/config"
)

type GenTokenReq struct {
	RoomId   string   `json:"room_id" validate:"required,require-valid-Id"`
	UserInfo UserInfo `json:"user_info" validate:"required"`
}

type UserInfo struct {
	Name         string       `json:"name" validate:"required"`
	UserId       string       `json:"user_id" validate:"required,require-valid-Id"`
	IsAdmin      bool         `json:"is_admin"`
	IsHidden     bool         `json:"is_hidden"`
	UserMetadata UserMetadata `json:"user_metadata" validate:"required"`
}

type UserMetadata struct {
	ProfilePic   string       `json:"profile_pic"`
	IsAdmin      bool         `json:"is_admin"`
	IsPresenter  bool         `json:"is_presenter"`
	RaisedHand   bool         `json:"raised_hand"`
	LockSettings LockSettings `json:"lock_settings"`
}

type LockSettings struct {
	LockMicrophone      *bool `json:"lock_microphone,omitempty"`
	LockWebcam          *bool `json:"lock_webcam,omitempty"`
	LockScreenSharing   *bool `json:"lock_screen_sharing,omitempty"`
	LockChat            *bool `json:"lock_chat,omitempty"`
	LockChatSendMessage *bool `json:"lock_chat_send_message,omitempty"`
	LockChatFileShare   *bool `json:"lock_chat_file_share,omitempty"`
	LockWhiteboard      *bool `json:"lock_whiteboard,omitempty"`
	LockSharedNotepad   *bool `json:"lock_shared_notepad,omitempty"`
}

type authTokenModel struct {
	app *config.AppConfig
	rs  *RoomService
}

func NewAuthTokenModel() *authTokenModel {
	return &authTokenModel{
		app: config.AppCnf,
		rs:  NewRoomService(),
	}
}

func (a *authTokenModel) DoGenerateToken(g *GenTokenReq) (string, error) {
	l := a.assignLockSettings(g)
	g.UserInfo.UserMetadata.LockSettings = *l
	a.makePresenter(g)

	metadata, err := json.Marshal(g.UserInfo.UserMetadata)
	if err != nil {
		return "", err
	}

	at := auth.NewAccessToken(a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret)
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		Room:      g.RoomId,
		RoomAdmin: g.UserInfo.IsAdmin,
		Hidden:    g.UserInfo.IsHidden,
	}

	at.AddGrant(grant).
		SetIdentity(g.UserInfo.UserId).
		SetName(g.UserInfo.Name).
		SetMetadata(string(metadata)).
		SetValidFor(a.app.LivekitInfo.TokenValidity)

	return at.ToJWT()
}

func (a *authTokenModel) assignLockSettings(g *GenTokenReq) *LockSettings {
	l := new(LockSettings)
	ul := g.UserInfo.UserMetadata.LockSettings

	if g.UserInfo.IsAdmin {
		// we'll keep this for future usage
		g.UserInfo.UserMetadata.IsAdmin = true
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
	} else {
		roomInfo, err := a.rs.LoadRoomInfoFromRedis(g.RoomId)
		if err != nil {
			return l
		}

		meta := make([]byte, len(roomInfo.Metadata))
		copy(meta, roomInfo.Metadata)

		m := new(RoomMetadata)
		_ = json.Unmarshal(meta, m)
		// if no lock settings were for this user
		// then we'll use default room lock settings
		dl := m.DefaultLockSettings

		if ul.LockWebcam == nil && dl.LockWebcam != nil {
			l.LockWebcam = dl.LockWebcam
		}
		if ul.LockMicrophone == nil && dl.LockMicrophone != nil {
			l.LockMicrophone = dl.LockMicrophone
		}
		if ul.LockScreenSharing == nil && dl.LockScreenSharing != nil {
			l.LockScreenSharing = dl.LockScreenSharing
		}
		if ul.LockChat == nil && dl.LockChat != nil {
			l.LockChat = dl.LockChat
		}
		if ul.LockChatSendMessage == nil && dl.LockChatSendMessage != nil {
			l.LockChatSendMessage = dl.LockChatSendMessage
		}
		if ul.LockChatFileShare == nil && dl.LockChatFileShare != nil {
			l.LockChatFileShare = dl.LockChatFileShare
		}
		if ul.LockWhiteboard == nil && dl.LockWhiteboard != nil {
			l.LockWhiteboard = dl.LockWhiteboard
		}
		if ul.LockSharedNotepad == nil && dl.LockSharedNotepad != nil {
			l.LockSharedNotepad = dl.LockSharedNotepad
		}
	}

	return l
}

func (a *authTokenModel) makePresenter(g *GenTokenReq) {
	if g.UserInfo.IsAdmin && !g.UserInfo.IsHidden {
		participants, err := a.rs.LoadParticipantsFromRedis(g.RoomId)
		if err != nil {
			return
		}
		hasPresenter := false
		for _, p := range participants {
			meta := make([]byte, len(p.Metadata))
			copy(meta, p.Metadata)
			m := new(UserMetadata)
			_ = json.Unmarshal(meta, m)
			if m.IsPresenter {
				hasPresenter = true
				break
			}
		}
		if !hasPresenter {
			g.UserInfo.UserMetadata.IsPresenter = true
		}
	}
}

// GenTokenForRecorder only for either recorder or RTMP bot
// Because we don't want to add any service settings which may
// prevent to work recorder/rtmp bot as expected.
func (a *authTokenModel) GenTokenForRecorder(g *GenTokenReq) (string, error) {
	at := auth.NewAccessToken(a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret)
	// basic permission
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		Room:      g.RoomId,
		RoomAdmin: false,
		Hidden:    g.UserInfo.IsHidden,
	}

	at.AddGrant(grant).
		SetIdentity(g.UserInfo.UserId).
		SetValidFor(a.app.LivekitInfo.TokenValidity)

	return at.ToJWT()
}

func (a *authTokenModel) Validation(g *GenTokenReq) []*config.ErrorResponse {
	return a.app.DoValidateReq(g)
}

type ValidateTokenReq struct {
	Token  string `json:"token"`
	RoomId string `json:"room_id"`
	Sid    string `json:"sid"`
	grant  *auth.APIKeyTokenVerifier
}

func (a *authTokenModel) DoValidateToken(v *ValidateTokenReq) (*auth.ClaimGrants, error) {
	grant, err := auth.ParseAPIToken(v.Token)
	if err != nil {
		return nil, err
	}

	claims, err := grant.Verify(a.app.LivekitInfo.Secret)
	if err != nil {
		return nil, err
	}

	v.grant = grant
	return claims, nil
}

func (a *authTokenModel) verifyTokenWithRoomInfo(v *ValidateTokenReq) (*auth.ClaimGrants, *RoomInfo, error) {
	claims, err := a.DoValidateToken(v)
	if err != nil {
		return nil, nil, err
	}

	if claims.Video.Room != v.RoomId {
		return nil, nil, errors.New("roomId didn't match")
	}

	m := NewRoomModel()
	roomInfo, _ := m.GetRoomInfo(claims.Video.Room, v.Sid, 1)
	if roomInfo.Id == 0 {
		return nil, nil, errors.New("room isn't actively running")
	}

	return claims, roomInfo, nil
}

// DoRenewToken we'll renew token
func (a *authTokenModel) DoRenewToken(v *ValidateTokenReq) (string, error) {
	claims, _, err := a.verifyTokenWithRoomInfo(v)
	if err != nil {
		return "", err
	}

	m := NewRoomService()
	// load current information
	p, err := m.LoadParticipantInfoFromRedis(claims.Video.Room, claims.Identity)
	if err != nil {
		return "", err
	}

	at := auth.NewAccessToken(a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret)

	at.AddGrant(claims.Video).
		SetIdentity(claims.Identity).
		SetName(p.Name).
		SetMetadata(p.Metadata).SetValidFor(a.app.LivekitInfo.TokenValidity)

	return at.ToJWT()
}
