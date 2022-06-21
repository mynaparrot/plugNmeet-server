package models

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/jordic/lti"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
	"net/url"
	"strings"
	"time"
)

type LTIV1 struct {
	authModel      *roomAuthModel
	authTokenModel *authTokenModel
}

type LtiClaims struct {
	UserId    string `json:"user_id"`
	Name      string `json:"name"`
	IsAdmin   bool   `json:"is_admin"`
	RoomId    string `json:"room_id"`
	RoomTitle string `json:"room_title"`
}

type LTIV1FetchRecordingsReq struct {
	From    int    `json:"from"`
	Limit   int    `json:"limit"`
	OrderBy string `json:"order_by"`
}

func NewLTIV1Model() *LTIV1 {
	return &LTIV1{
		authModel:      NewRoomAuthModel(),
		authTokenModel: NewAuthTokenModel(),
	}
}

func (m *LTIV1) Landing(c *fiber.Ctx, requests, signingURL string) error {
	params, err := m.VerifyAuth(requests, signingURL)
	if err != nil {
		return err
	}

	roomId := fmt.Sprintf("%s_%s_%s", params.Get("tool_consumer_instance_guid"), params.Get("context_id"), params.Get("resource_link_id"))

	claims := &LtiClaims{
		UserId:    m.genUserId(params.Get("lis_person_contact_email_primary")),
		Name:      params.Get("lis_person_name_full"),
		IsAdmin:   false,
		RoomId:    roomId,
		RoomTitle: params.Get("context_label"),
	}

	if strings.Contains(params.Get("roles"), "Instructor") {
		claims.IsAdmin = true
	}

	fmt.Println(claims)
	fmt.Println(params.Get("roles"))

	j, err := m.ToJWT(claims)
	if err != nil {
		return err
	}

	return c.Render("assets/lti/v1", fiber.Map{
		"Title":   claims.RoomTitle,
		"Token":   j,
		"IsAdmin": claims.IsAdmin,
	})
}

func (m *LTIV1) VerifyAuth(requests, signingURL string) (*url.Values, error) {
	r := strings.Split(requests, "&")
	p := lti.NewProvider(config.AppCnf.Client.Secret, signingURL)
	p.Method = "POST"
	p.ConsumerKey = config.AppCnf.Client.ApiKey
	var providedSignature string

	for _, f := range r {
		t := strings.Split(f, "=")
		b, _ := url.QueryUnescape(t[1])
		if t[0] == "oauth_signature" {
			providedSignature = b
		} else {
			p.Add(t[0], b)
		}
	}

	if p.Get("oauth_consumer_key") != p.ConsumerKey {
		return nil, errors.New("invalid consumer_key")
	}

	sign, err := p.Sign()
	if err != nil {
		return nil, err
	}
	params := p.Params()

	if sign != providedSignature {
		return nil, errors.New("verification failed")
	}

	return &params, nil
}

func (m *LTIV1) genUserId(email string) string {
	hasher := md5.New()
	hasher.Write([]byte(email))
	sha := hex.EncodeToString(hasher.Sum(nil))

	return sha
}

func (m *LTIV1) ToJWT(c *LtiClaims) (string, error) {
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(config.AppCnf.Client.Secret)},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    config.AppCnf.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now()),
		Expiry:    jwt.NewNumericDate(time.Now().Add(time.Hour * 2)), // valid for 2 hours
		Subject:   c.UserId,
	}

	return jwt.Signed(sig).Claims(cl).Claims(c).CompactSerialize()
}

func (m *LTIV1) LTIV1VerifyHeaderToken(token string) (*LtiClaims, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return nil, err
	}

	out := jwt.Claims{}
	claims := &LtiClaims{}
	if err = tok.Claims([]byte(config.AppCnf.Client.Secret), &out, claims); err != nil {
		return nil, err
	}
	if err = out.Validate(jwt.Expected{Issuer: config.AppCnf.Client.ApiKey, Time: time.Now()}); err != nil {
		return nil, err
	}

	return claims, nil
}

func (m *LTIV1) LTIV1JoinRoom(c *LtiClaims) (string, error) {
	active, _ := m.authModel.IsRoomActive(&IsRoomActiveReq{
		RoomId: c.RoomId,
	})

	if !active {
		status, msg, _ := m.createRoomSession(c)
		if !status {
			return "", errors.New(msg)
		}
	}

	token, err := m.joinRoom(c)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *LTIV1) createRoomSession(c *LtiClaims) (bool, string, *livekit.Room) {
	req := &RoomCreateReq{
		RoomId: c.RoomId,
		RoomMetadata: RoomMetadata{
			RoomTitle: c.RoomTitle,
			Features: RoomCreateFeatures{
				AllowWebcams:               true,
				AllowScreenShare:           true,
				AllowRecording:             true,
				AllowRTMP:                  true,
				AllowViewOtherWebcams:      true,
				AllowViewOtherParticipants: true,
				AllowPolls:                 true,
				ChatFeatures: ChatFeatures{
					AllowChat:       true,
					AllowFileUpload: true,
				},
				SharedNotePadFeatures: SharedNotePadFeatures{
					AllowedSharedNotePad: true,
				},
				WhiteboardFeatures: WhiteboardFeatures{
					AllowedWhiteboard: true,
				},
				ExternalMediaPlayerFeatures: ExternalMediaPlayerFeatures{
					AllowedExternalMediaPlayer: true,
				},
				BreakoutRoomFeatures: BreakoutRoomFeatures{
					IsAllow: true,
				},
			},
		},
	}

	return m.authModel.CreateRoom(req)
}

func (m *LTIV1) joinRoom(c *LtiClaims) (string, error) {
	token, err := m.authTokenModel.DoGenerateToken(&GenTokenReq{
		RoomId: c.RoomId,
		UserInfo: UserInfo{
			UserId:  c.UserId,
			Name:    c.Name,
			IsAdmin: c.IsAdmin,
			UserMetadata: UserMetadata{
				IsAdmin: c.IsAdmin,
			},
		},
	})
	if err != nil {
		return "", err
	}

	return token, nil
}
