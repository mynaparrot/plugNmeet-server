package natscontroller

import (
	"context"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/auth"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	log "github.com/sirupsen/logrus"
)

type NatsAuthController struct {
	ctx           context.Context
	app           *config.AppConfig
	authModel     *authmodel.AuthModel
	natsService   *natsservice.NatsService
	js            jetstream.JetStream
	issuerKeyPair nkeys.KeyPair
}

func NewNatsAuthController(app *config.AppConfig, authModel *authmodel.AuthModel, kp nkeys.KeyPair, js jetstream.JetStream) *NatsAuthController {
	return &NatsAuthController{
		ctx:           context.Background(),
		app:           app,
		authModel:     authModel,
		natsService:   natsservice.New(app),
		js:            js,
		issuerKeyPair: kp,
	}
}

func (s *NatsAuthController) Handle(r micro.Request) {
	rc, err := jwt.DecodeAuthorizationRequestClaims(string(r.Data()))
	if err != nil {
		log.Println("Error", err)
		_ = r.Error("500", err.Error(), nil)
	}
	userNkey := rc.UserNkey
	serverId := rc.Server.ID

	claims, err := s.handleClaims(rc)
	if err != nil {
		s.Respond(r, userNkey, serverId, "", err)
		return
	}

	token, err := ValidateAndSign(claims, s.issuerKeyPair)
	s.Respond(r, userNkey, serverId, token, err)
}

func (s *NatsAuthController) handleClaims(req *jwt.AuthorizationRequestClaims) (*jwt.UserClaims, error) {
	claims := jwt.NewUserClaims(req.UserNkey)
	claims.Audience = s.app.NatsInfo.Account

	// check the info first
	data, err := s.authModel.VerifyPlugNmeetAccessToken(req.ConnectOptions.Token)
	if err != nil {
		return nil, err
	}

	roomId := data.GetRoomId()
	userId := data.GetUserId()

	userInfo, err := s.natsService.GetUserInfo(userId)
	if err != nil {
		return nil, err
	}
	if userInfo == nil {
		return nil, errors.New("user not found in the list")
	}

	allow := jwt.StringList{
		"$JS.API.INFO",
		fmt.Sprintf("$JS.API.STREAM.INFO.%s", roomId),
		// allow sending messages to the system
		fmt.Sprintf("%s.%s.%s", s.app.NatsInfo.Subjects.SystemWorker, roomId, userId),
	}

	publicChatPermission, err := s.natsService.CreatePublicChatConsumer(roomId, userId)
	if err != nil {
		return nil, err
	}
	allow.Add(publicChatPermission...)

	privateChatPermission, err := s.natsService.CreatePrivateChatConsumer(roomId, userId)
	if err != nil {
		return nil, err
	}
	allow.Add(privateChatPermission...)

	sysPublicPermission, err := s.natsService.CreateSystemPublicConsumer(roomId, userId)
	if err != nil {
		return nil, err
	}
	allow.Add(sysPublicPermission...)

	sysPrivatePermission, err := s.natsService.CreateSystemPrivateConsumer(roomId, userId)
	if err != nil {
		return nil, err
	}
	allow.Add(sysPrivatePermission...)

	whiteboardPermission, err := s.natsService.CreateWhiteboardConsumer(roomId, userId)
	if err != nil {
		return nil, err
	}
	allow.Add(whiteboardPermission...)

	// put name in proper format
	claims.Name = fmt.Sprintf("%s:%s", roomId, userId)
	// Assign Permissions
	claims.Permissions = jwt.Permissions{
		Pub: jwt.Permission{
			Allow: allow,
		},
	}

	return claims, nil
}

func (s *NatsAuthController) Respond(req micro.Request, userNKey, serverId, userJWT string, err error) {
	rc := jwt.NewAuthorizationResponseClaims(userNKey)
	rc.Audience = serverId
	rc.Jwt = userJWT
	if err != nil {
		fmt.Println(err)
		rc.Error = err.Error()
	}

	token, err := rc.Encode(s.issuerKeyPair)
	if err != nil {
		log.Errorln("error encoding response jwt:", err)
	}

	_ = req.Respond([]byte(token))
}

func ValidateAndSign(claims *jwt.UserClaims, kp nkeys.KeyPair) (string, error) {
	// Validate the claims.
	vr := jwt.CreateValidationResults()
	claims.Validate(vr)
	if len(vr.Errors()) > 0 {
		return "", errors.Join(vr.Errors()...)
	}

	// Sign it with the issuer key since this is non-operator mode.
	return claims.Encode(kp)
}
