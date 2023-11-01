package models

import (
	"errors"
	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"time"
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

type AccessTokenUserInfo struct {
	Name   string `json:"name"`
	RoomId string `json:"roomId"`
}

func (a *AuthTokenModel) GeneratePlugNmeetToken(g *plugnmeet.GenerateTokenReq) (string, error) {
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
		RoomId:   g.RoomId,
		IsAdmin:  g.UserInfo.IsAdmin,
		IsHidden: g.UserInfo.IsHidden,
	}

	return a.generatePlugNmeetJWTToken(g.UserInfo.UserId, c)
}

func (a *AuthTokenModel) generatePlugNmeetJWTToken(userId string, c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(a.app.Client.Secret)},
		(&jose.SignerOptions{}).WithType("JWT"))

	if err != nil {
		return "", err
	}

	cl := &jwt.Claims{
		Issuer:    a.app.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now()),
		Expiry:    jwt.NewNumericDate(time.Now().Add(a.app.LivekitInfo.TokenValidity)),
		Subject:   userId,
	}
	return jwt.Signed(sig).Claims(cl).Claims(c).CompactSerialize()
}

// GenerateLivekitToken will generate token to join livekit server
// It will use info as other validation. We just don't want user simple copy/past token from url
// instated plugNmeet-server will generate once validation will be completed.
func (a *AuthTokenModel) GenerateLivekitToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	metadata, err := a.rs.ManageRoomWithUsersMetadata(c.RoomId, c.UserId, "get", "")
	if err != nil {
		return "", err
	}

	at := auth.NewAccessToken(a.app.LivekitInfo.ApiKey, a.app.LivekitInfo.Secret)
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		Room:      c.RoomId,
		RoomAdmin: c.IsAdmin,
		Hidden:    c.IsHidden,
	}

	at.AddGrant(grant).
		SetIdentity(c.UserId).
		SetName(c.Name).
		SetMetadata(metadata).
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

	m := NewRoomService()
	// load current information
	p, err := m.ManageActiveUsersList(claims.RoomId, claims.UserId, "get", 0)
	if err != nil {
		return "", err
	}
	if len(p) == 0 {
		return "", errors.New("user isn't online")
	}

	return a.generatePlugNmeetJWTToken(claims.UserId, claims)
}

func (a *AuthTokenModel) VerifyPlugNmeetAccessToken(token string) (*plugnmeet.PlugNmeetTokenClaims, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return nil, err
	}

	out := jwt.Claims{}
	claims := plugnmeet.PlugNmeetTokenClaims{}
	if err = tok.Claims([]byte(a.app.Client.Secret), &out, &claims); err != nil {
		return nil, err
	}

	if err = out.Validate(jwt.Expected{Issuer: a.app.Client.ApiKey, Time: time.Now()}); err != nil {
		return nil, err
	}
	claims.UserId = out.Subject

	return &claims, nil
}

// ValidateLivekitWebhookToken can be use to validate both livekit & plugnmeet token
func (a *AuthTokenModel) ValidateLivekitWebhookToken(token string) (*auth.ClaimGrants, error) {
	grant, err := auth.ParseAPIToken(token)
	if err != nil {
		return nil, err
	}

	claims, err := grant.Verify(a.app.LivekitInfo.Secret)
	if err != nil {
		return nil, err
	}

	return claims, nil
}
