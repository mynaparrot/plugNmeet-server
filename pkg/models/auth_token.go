package models

import (
	"errors"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

func (a *AuthTokenModel) DoGenerateToken(g *plugnmeet.GenerateTokenReq) (string, error) {
	if g.UserInfo.UserMetadata == nil {
		g.UserInfo.UserMetadata = new(plugnmeet.UserMetadata)
	}

	a.assignLockSettings(g)
	if g.UserInfo.IsAdmin {
		a.makePresenter(g)
	}

	metadata, err := json.Marshal(g.UserInfo.UserMetadata)
	if err != nil {
		return "", err
	}

	at := auth.NewAccessToken(a.app.Client.ApiKey, a.app.Client.Secret)
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		Room:      g.RoomId,
		RoomAdmin: g.UserInfo.IsAdmin,
		Hidden:    g.UserInfo.IsHidden,
	}

	if g.UserInfo.UserId == config.RECORDER_BOT || g.UserInfo.UserId == config.RTMP_BOT {
		grant.Recorder = true
	}

	at.AddGrant(grant).
		SetIdentity(g.UserInfo.UserId).
		SetName(g.UserInfo.Name).
		SetMetadata(string(metadata)).
		SetValidFor(a.app.LivekitInfo.TokenValidity)

	return at.ToJWT()
}

// GenerateLivekitToken will generate token to join livekit server
// It will use info as other validation. We just don't want user simple copy/past token from url
// instated plugNmeet-server will generate once validation will be completed.
func (a *AuthTokenModel) GenerateLivekitToken(claims *auth.ClaimGrants) (string, error) {
	at := auth.NewAccessToken(a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret)
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		Room:      claims.Video.Room,
		RoomAdmin: claims.Video.RoomAdmin,
		Hidden:    claims.Video.Hidden,
	}

	at.AddGrant(grant).
		SetIdentity(claims.Identity).
		SetName(claims.Name).
		SetMetadata(claims.Metadata).
		SetValidFor(a.app.LivekitInfo.TokenValidity)

	return at.ToJWT()
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
	if ul.LockPrivateChat == nil && dl.LockPrivateChat != nil {
		l.LockPrivateChat = dl.LockPrivateChat
	}
	if ul.LockWhiteboard == nil && dl.LockWhiteboard != nil {
		l.LockWhiteboard = dl.LockWhiteboard
	}
	if ul.LockSharedNotepad == nil && dl.LockSharedNotepad != nil {
		l.LockSharedNotepad = dl.LockSharedNotepad
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

			m := new(plugnmeet.UserMetadata)
			_ = json.Unmarshal(meta, m)

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

// GenTokenForRecorder only for either recorder or RTMP bot
// Because we don't want to add any service settings which may
// prevent to work recorder/rtmp bot as expected.
func (a *AuthTokenModel) GenTokenForRecorder(g *plugnmeet.GenerateTokenReq) (string, error) {
	at := auth.NewAccessToken(a.app.Client.ApiKey, a.app.Client.Secret)
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

type ValidateTokenReq struct {
	Token  string `json:"token"`
	RoomId string `json:"room_id"`
	Sid    string `json:"sid"`
	grant  *auth.APIKeyTokenVerifier
}

// DoValidateToken can be use to validate both livekit & plugnmeet token
func (a *AuthTokenModel) DoValidateToken(v *ValidateTokenReq, livekit bool) (*auth.ClaimGrants, error) {
	grant, err := auth.ParseAPIToken(v.Token)
	if err != nil {
		return nil, err
	}

	secret := a.app.Client.Secret
	if livekit {
		secret = a.app.LivekitInfo.Secret
	}

	claims, err := grant.Verify(secret)
	if err != nil {
		return nil, err
	}

	v.grant = grant
	return claims, nil
}

func (a *AuthTokenModel) verifyTokenWithRoomInfo(v *ValidateTokenReq) (*auth.ClaimGrants, *RoomInfo, error) {
	claims, err := a.DoValidateToken(v, false)
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
func (a *AuthTokenModel) DoRenewToken(v *ValidateTokenReq) (string, error) {
	claims, _, err := a.verifyTokenWithRoomInfo(v)
	if err != nil {
		return "", err
	}

	m := NewRoomService()
	// load current information
	p, err := m.LoadParticipantInfo(claims.Video.Room, claims.Identity)
	if err != nil {
		return "", err
	}

	at := auth.NewAccessToken(a.app.Client.ApiKey, a.app.Client.Secret)
	at.AddGrant(claims.Video).
		SetIdentity(claims.Identity).
		SetName(p.Name).
		SetMetadata(p.Metadata).SetValidFor(a.app.LivekitInfo.TokenValidity)

	return at.ToJWT()
}
