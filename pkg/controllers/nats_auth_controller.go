package controllers

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	"github.com/sirupsen/logrus"
)

type NatsAuthController struct {
	app           *config.AppConfig
	authModel     *models.AuthModel
	natsService   *natsservice.NatsService
	issuerKeyPair nkeys.KeyPair
	curveKeyPair  nkeys.KeyPair
	logger        *logrus.Entry
}

func NewNatsAuthController(app *config.AppConfig, natsService *natsservice.NatsService, authModel *models.AuthModel, issuerKeyPair nkeys.KeyPair, curveKeyPair nkeys.KeyPair, logger *logrus.Entry) *NatsAuthController {
	return &NatsAuthController{
		app:           app,
		authModel:     authModel,
		natsService:   natsService,
		issuerKeyPair: issuerKeyPair,
		curveKeyPair:  curveKeyPair,
		logger:        logger.WithField("sub-controller", "nats-auth"),
	}
}

func (s *NatsAuthController) Handle(r micro.Request) {
	var data []byte
	var err error

	xKey := r.Headers().Get("Nats-Server-Xkey")
	if len(xKey) > 0 {
		if s.curveKeyPair == nil {
			s.logger.Errorln("received encrypted data from nats server but curveKeyPair is nil")
			_ = r.Error("500", "xKey not supported", nil)
			return
		}

		data, err = s.curveKeyPair.Open(r.Data(), xKey)
		if err != nil {
			s.logger.WithError(err).Errorln("error decrypting message from nats server")
			_ = r.Error("500", err.Error(), nil)
			return
		}
	} else {
		data = r.Data()
	}

	rc, err := jwt.DecodeAuthorizationRequestClaims(string(data))
	if err != nil {
		s.logger.WithError(err).Errorln("error decoding authorization request")
		_ = r.Error("500", err.Error(), nil)
		return
	}

	userNkey := rc.UserNkey
	serverId := rc.Server.ID

	claims, err := s.handleClaims(rc)
	if err != nil {
		s.logger.WithError(err).Errorln("error handling claims")
		s.respond(r, userNkey, serverId, "", err)
		return
	}

	token, err := s.validateAndSign(claims, s.issuerKeyPair)
	s.respond(r, userNkey, serverId, token, err)
}

func (s *NatsAuthController) handleClaims(req *jwt.AuthorizationRequestClaims) (*jwt.UserClaims, error) {
	claims := jwt.NewUserClaims(req.UserNkey)
	claims.Audience = s.app.NatsInfo.Account

	// nats v2.10.28 & v2.11.2 (#6808) Auth tokens are now redacted
	// but for our case it is necessary to have it, so we'll add here
	// otherwise user CONNECT & DISCONNECT logics will be breaking
	claims.Name = req.ConnectOptions.Token

	// check the info first
	data, err := s.authModel.VerifyPlugNmeetAccessToken(req.ConnectOptions.Token, 0)
	if err != nil {
		return nil, err
	}

	if data.GetName() == config.RecorderUserAuthName {
		s.setPermissionForRecorder(claims)
		return claims, nil
	}

	err = s.setPermissionForClient(data, claims)
	if err != nil {
		return nil, err
	}

	return claims, nil
}

func (s *NatsAuthController) setPermissionForRecorder(claims *jwt.UserClaims) {
	pubAllow := jwt.StringList{
		"$JS.API.INFO",
		"_INBOX.>", // otherwise won't be able to send respond msg
		fmt.Sprintf("$JS.API.STREAM.INFO.KV_%s", s.app.NatsInfo.Recorder.RecorderInfoKv),
		fmt.Sprintf("$KV.%s.>", s.app.NatsInfo.Recorder.RecorderInfoKv),
		fmt.Sprintf("$JS.API.DIRECT.GET.KV_%s.>", s.app.NatsInfo.Recorder.RecorderInfoKv),
		// Allow publishing the job to the stream
		s.app.NatsInfo.Recorder.TranscodingJobs,
		// Allow fetching the next message from the consumer & send ack
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s", s.app.NatsInfo.Recorder.TranscodingJobs, transcoderConsumerDurable),
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s", s.app.NatsInfo.Recorder.TranscodingJobs, transcoderConsumerDurable),
		fmt.Sprintf("$JS.ACK.%s.%s.>", s.app.NatsInfo.Recorder.TranscodingJobs, transcoderConsumerDurable),
	}

	claims.Permissions = jwt.Permissions{
		Pub: jwt.Permission{
			Allow: pubAllow,
		},
		Sub: jwt.Permission{
			Allow: jwt.StringList{
				s.app.NatsInfo.Recorder.RecorderChannel,
				"_INBOX.>",
			},
		},
	}
}

func (s *NatsAuthController) setPermissionForClient(data *plugnmeet.PlugNmeetTokenClaims, claims *jwt.UserClaims) error {
	roomId := data.GetRoomId()
	userId := data.GetUserId()

	userInfo, err := s.natsService.GetUserInfo(roomId, userId)
	if err != nil {
		return err
	}
	if userInfo == nil {
		return fmt.Errorf("user info not found for userId: %s, roomId: %s", userId, roomId)
	}

	stream, durableName, err := s.natsService.CreateUserConsumer(roomId, userId)
	if err != nil {
		return err
	}

	// Assign Permissions
	claims.Permissions = jwt.Permissions{
		Pub: jwt.Permission{
			Allow: jwt.StringList{
				"$JS.API.INFO",
				// permission for consumer (JetStream)
				fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s", stream, durableName),
				fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s", stream, durableName),
				fmt.Sprintf("$JS.ACK.%s.%s.>", stream, durableName),

				// permission to publish messages to the system (JetStream)
				fmt.Sprintf("%s.%s.%s", s.app.NatsInfo.Subjects.SystemJsWorker, roomId, userId),
				// permission to publish in core pub/sub
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Chat, roomId),
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Whiteboard, roomId),
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.DataChannel, roomId),
			},
		},
		Sub: jwt.Permission{
			Allow: jwt.StringList{
				"_INBOX.>", // otherwise break request-reply patterns
				// allow to subscribe in pub/sub channel system public which is different from JetStream
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.SystemPublic, roomId),
				// other pub/sub channels
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Chat, roomId),
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Whiteboard, roomId),
				fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.DataChannel, roomId),
			},
		},
	}

	return nil
}

func (s *NatsAuthController) respond(req micro.Request, userNKey, serverId, userJWT string, err error) {
	rc := jwt.NewAuthorizationResponseClaims(userNKey)
	rc.Audience = serverId
	rc.Jwt = userJWT
	if err != nil {
		rc.Error = err.Error()
	}

	token, err := rc.Encode(s.issuerKeyPair)
	if err != nil {
		s.logger.WithError(err).Errorln("error encoding response jwt")
		_ = req.Respond(nil)
		return
	}
	data := []byte(token)

	// Check if encryption is required.
	xKey := req.Headers().Get("Nats-Server-Xkey")
	if len(xKey) > 0 {
		data, err = s.curveKeyPair.Seal(data, xKey)
		if err != nil {
			s.logger.WithError(err).Errorln("error encrypting response JWT")
			_ = req.Respond(nil)
			return
		}
	}

	_ = req.Respond(data)
}

func (s *NatsAuthController) validateAndSign(claims *jwt.UserClaims, kp nkeys.KeyPair) (string, error) {
	// Validate the claims.
	vr := jwt.CreateValidationResults()
	claims.Validate(vr)
	if len(vr.Errors()) > 0 {
		return "", errors.Join(vr.Errors()...)
	}

	// Sign it with the issuer key since this is non-operator mode.
	return claims.Encode(kp)
}
