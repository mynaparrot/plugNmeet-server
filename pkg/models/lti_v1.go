package models

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/jordic/lti"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type LTIV1 struct {
	authModel      *roomAuthModel
	authTokenModel *authTokenModel
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

	claims := &LtiClaims{
		UserId:    userId,
		Name:      name,
		IsAdmin:   false,
		RoomId:    m.genHashId(roomId),
		RoomTitle: params.Get("context_label"),
	}

	if strings.Contains(params.Get("roles"), "Instructor") {
		claims.IsAdmin = true
	}
	AssignLTIV1CustomParams(params, claims)

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
	req := PrepareLTIV1RoomCreateReq(c)
	return m.authModel.CreateRoom(req)
}

func (m *LTIV1) joinRoom(c *LtiClaims) (string, error) {
	token, err := m.authTokenModel.DoGenerateToken(&plugnmeet.GenerateTokenReq{
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

func AssignLTIV1CustomParams(params *url.Values, claims *LtiClaims) {
	b := new(bool)
	customPara := new(LtiCustomParameters)

	if params.Get("custom_room_duration") != "" {
		duration, _ := strconv.Atoi(params.Get("custom_room_duration"))
		customPara.RoomDuration = uint64(duration)
	}
	if params.Get("custom_allow_polls") == "false" {
		customPara.AllowPolls = b
	}
	if params.Get("custom_allow_shared_note_pad") == "false" {
		customPara.AllowSharedNotePad = b
	}
	if params.Get("custom_allow_breakout_room") == "false" {
		customPara.AllowBreakoutRoom = b
	}
	if params.Get("custom_allow_recording") == "false" {
		customPara.AllowRecording = b
	}
	if params.Get("custom_allow_rtmp") == "false" {
		customPara.AllowRTMP = b
	}
	if params.Get("custom_allow_view_other_webcams") == "false" {
		customPara.AllowViewOtherWebcams = b
	}
	if params.Get("custom_allow_view_other_users_list") == "false" {
		customPara.AllowViewOtherParticipants = b
	}
	// this should be last bool
	if params.Get("custom_mute_on_start") == "true" {
		*b = true
		customPara.MuteOnStart = b
	}

	// custom design
	customDesign := new(LtiCustomDesign)
	if params.Get("custom_primary_color") != "" {
		customDesign.PrimaryColor = params.Get("custom_primary_color")
	}
	if params.Get("custom_secondary_color") != "" {
		customDesign.SecondaryColor = params.Get("custom_secondary_color")
	}
	if params.Get("custom_background_color") != "" {
		customDesign.BackgroundColor = params.Get("custom_background_color")
	}
	if params.Get("custom_custom_logo") != "" {
		customDesign.CustomLogo = params.Get("custom_custom_logo")
	}

	claims.LtiCustomParameters = customPara
	claims.LtiCustomParameters.LtiCustomDesign = customDesign
}

func PrepareLTIV1RoomCreateReq(c *LtiClaims) *plugnmeet.CreateRoomReq {
	req := &plugnmeet.CreateRoomReq{
		RoomId: c.RoomId,
		Metadata: &plugnmeet.RoomMetadata{
			RoomTitle: c.RoomTitle,
			RoomFeatures: &plugnmeet.RoomCreateFeatures{
				AllowWebcams:            true,
				AllowScreenShare:        true,
				AllowRecording:          true,
				AllowRtmp:               true,
				AllowViewOtherWebcams:   true,
				AllowViewOtherUsersList: true,
				AllowPolls:              true,
				ChatFeatures: &plugnmeet.ChatFeatures{
					AllowChat:       true,
					AllowFileUpload: true,
				},
				SharedNotePadFeatures: &plugnmeet.SharedNotePadFeatures{
					AllowedSharedNotePad: true,
				},
				WhiteboardFeatures: &plugnmeet.WhiteboardFeatures{
					AllowedWhiteboard: true,
				},
				ExternalMediaPlayerFeatures: &plugnmeet.ExternalMediaPlayerFeatures{
					AllowedExternalMediaPlayer: true,
				},
				BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{
					IsAllow: true,
				},
				DisplayExternalLinkFeatures: &plugnmeet.DisplayExternalLinkFeatures{
					IsAllow: true,
				},
			},
		},
	}

	if c.LtiCustomParameters != nil {
		p := c.LtiCustomParameters
		f := req.Metadata.RoomFeatures

		if p.RoomDuration > 0 {
			f.RoomDuration = &p.RoomDuration
		}
		if p.MuteOnStart != nil {
			f.MuteOnStart = *p.MuteOnStart
		}
		if p.AllowSharedNotePad != nil {
			f.SharedNotePadFeatures.AllowedSharedNotePad = *p.AllowSharedNotePad
		}
		if p.AllowBreakoutRoom != nil {
			f.BreakoutRoomFeatures.IsAllow = *p.AllowBreakoutRoom
		}
		if p.AllowPolls != nil {
			f.AllowPolls = *p.AllowPolls
		}
		if p.AllowRecording != nil {
			f.AllowRecording = *p.AllowRecording
		}
		if p.AllowRTMP != nil {
			f.AllowRtmp = *p.AllowRTMP
		}
		if p.AllowViewOtherWebcams != nil {
			f.AllowViewOtherWebcams = *p.AllowViewOtherWebcams
		}
		if p.AllowViewOtherParticipants != nil {
			f.AllowViewOtherUsersList = *p.AllowViewOtherParticipants
		}

		req.Metadata.RoomFeatures = f
	}

	return req
}
