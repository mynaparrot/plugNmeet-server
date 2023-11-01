package models

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/jordic/lti"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"net/url"
	"strings"
	"time"
)

type LTIV1 struct {
	authModel      *RoomAuthModel
	authTokenModel *AuthTokenModel
}

type LtiClaims struct {
	UserId              string               `json:"user_id"`
	Name                string               `json:"name"`
	IsAdmin             bool                 `json:"is_admin"`
	RoomId              string               `json:"room_id"`
	RoomTitle           string               `json:"room_title"`
	LtiCustomParameters *LtiCustomParameters `json:"lti_custom_parameters,omitempty"`
}

type LtiCustomParameters struct {
	RoomDuration               uint64           `json:"room_duration,omitempty"`
	AllowPolls                 *bool            `json:"allow_polls,omitempty"`
	AllowSharedNotePad         *bool            `json:"allow_shared_note_pad,omitempty"`
	AllowBreakoutRoom          *bool            `json:"allow_breakout_room,omitempty"`
	AllowRecording             *bool            `json:"allow_recording,omitempty"`
	AllowRTMP                  *bool            `json:"allow_rtmp,omitempty"`
	AllowViewOtherWebcams      *bool            `json:"allow_view_other_webcams,omitempty"`
	AllowViewOtherParticipants *bool            `json:"allow_view_other_users_list,omitempty"`
	MuteOnStart                *bool            `json:"mute_on_start,omitempty"`
	LtiCustomDesign            *LtiCustomDesign `json:"lti_custom_design,omitempty"`
}

type LtiCustomDesign struct {
	PrimaryColor    string `json:"primary_color,omitempty"`
	SecondaryColor  string `json:"secondary_color,omitempty"`
	BackgroundColor string `json:"background_color,omitempty"`
	CustomLogo      string `json:"custom_logo,omitempty"`
}

type LTIV1FetchRecordingsReq struct {
	From    uint32 `json:"from"`
	Limit   uint32 `json:"limit"`
	OrderBy string `json:"order_by"`
}

func NewLTIV1Model() *LTIV1 {
	return &LTIV1{
		authModel:      NewRoomAuthModel(),
		authTokenModel: NewAuthTokenModel(),
	}
}

func (m *LTIV1) LTIV1Landing(c *fiber.Ctx, requests, signingURL string) error {
	params, err := m.VerifyAuth(requests, signingURL)
	if err != nil {
		return err
	}

	roomId := fmt.Sprintf("%s_%s_%s", params.Get("tool_consumer_instance_guid"), params.Get("context_id"), params.Get("resource_link_id"))

	userId := params.Get("user_id")
	if userId == "" {
		userId = m.genHashId(params.Get("lis_person_contact_email_primary"))
	}

	if userId == "" {
		return errors.New("either value of user_id or lis_person_contact_email_primary  required")
	}

	name := params.Get("lis_person_name_full")
	if name == "" {
		name = fmt.Sprintf("%s_%s", "User", userId)
	}

	claims := &plugnmeet.LtiClaims{
		UserId:    userId,
		Name:      name,
		IsAdmin:   false,
		RoomId:    m.genHashId(roomId),
		RoomTitle: params.Get("context_label"),
	}

	if strings.Contains(params.Get("roles"), "Instructor") {
		claims.IsAdmin = true
	}
	utils.AssignLTIV1CustomParams(params, claims)

	j, err := m.ToJWT(claims)
	if err != nil {
		return err
	}

	vals := fiber.Map{
		"Title":   claims.RoomTitle,
		"Token":   j,
		"IsAdmin": claims.IsAdmin,
	}

	if claims.LtiCustomParameters.LtiCustomDesign != nil {
		design, err := json.Marshal(claims.LtiCustomParameters.LtiCustomDesign)
		if err == nil {
			vals["CustomDesign"] = string(design)
		}
	}

	return c.Render("assets/lti/v1", vals)
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
		log.Errorln("Calculated: " + sign + " provided: " + providedSignature)
		return nil, errors.New("verification failed")
	}

	return &params, nil
}

func (m *LTIV1) genHashId(id string) string {
	hasher := sha1.New()
	hasher.Write([]byte(id))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return hash
}

func (m *LTIV1) ToJWT(c *plugnmeet.LtiClaims) (string, error) {
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(config.AppCnf.Client.Secret)},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    config.AppCnf.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now().UTC()),
		Expiry:    jwt.NewNumericDate(time.Now().UTC().Add(time.Hour * 2)), // valid for 2 hours
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
	if err = out.Validate(jwt.Expected{Issuer: config.AppCnf.Client.ApiKey, Time: time.Now().UTC()}); err != nil {
		return nil, err
	}

	return claims, nil
}

func (m *LTIV1) LTIV1JoinRoom(c *plugnmeet.LtiClaims) (string, error) {
	active, _, _ := m.authModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{
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

func (m *LTIV1) createRoomSession(c *plugnmeet.LtiClaims) (bool, string, *livekit.Room) {
	req := utils.PrepareLTIV1RoomCreateReq(c)
	return m.authModel.CreateRoom(req)
}

func (m *LTIV1) joinRoom(c *plugnmeet.LtiClaims) (string, error) {
	token, err := m.authTokenModel.GeneratePlugNmeetAccessToken(&plugnmeet.GenerateTokenReq{
		RoomId: c.RoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:  c.UserId,
			Name:    c.Name,
			IsAdmin: c.IsAdmin,
			UserMetadata: &plugnmeet.UserMetadata{
				IsAdmin: c.IsAdmin,
			},
		},
	})
	if err != nil {
		return "", err
	}

	return token, nil
}
