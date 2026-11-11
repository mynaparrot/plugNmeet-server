package controllers

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
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
			_ = r.Error("500", config.ErrAuthUnexpected.Error(), nil)
			return
		}

		data, err = s.curveKeyPair.Open(r.Data(), xKey)
		if err != nil {
			s.logger.WithError(err).Errorln("error decrypting message from nats server")
			_ = r.Error("500", config.ErrAuthUnexpected.Error(), nil)
			return
		}
	} else {
		data = r.Data()
	}

	rc, err := jwt.DecodeAuthorizationRequestClaims(string(data))
	if err != nil {
		s.logger.WithError(err).Errorln("error decoding authorization request")
		_ = r.Error("500", config.ErrAuthUnexpected.Error(), nil)
		return
	}

	userNkey := rc.UserNkey
	serverId := rc.Server.ID

	claims, err := s.handleClaims(rc)
	if err != nil {
		// operational failures are logged where they happen, so only the key is
		// sent back here
		s.respond(r, userNkey, serverId, "", err)
		return
	}

	token, err := s.validateAndSign(claims, s.issuerKeyPair)
	if err != nil {
		s.logger.WithError(err).Errorln("error validating or signing user claims")
		err = config.ErrAuthUnexpected
	}
	s.respond(r, userNkey, serverId, token, err)
}

func (s *NatsAuthController) handleClaims(req *jwt.AuthorizationRequestClaims) (*jwt.UserClaims, error) {
	claims := jwt.NewUserClaims(req.UserNkey)
	claims.Audience = s.app.NatsInfo.Account

	// check the info first
	data, err := s.authModel.VerifyPlugNmeetAccessToken(req.ConnectOptions.Token, 0)
	if err != nil {
		return nil, models.TokenVerifyError(err)
	}

	marshal, err := protojson.Marshal(data)
	if err != nil {
		s.logger.WithError(err).Errorln("error marshalling token claims")
		return nil, config.ErrAuthUnexpected
	}
	// Before we were using Auth tokens as default name but NATS v2.10.28 & v2.11.2 (#6808) Auth tokens are now redacted.
	// So we'll add plain user info here otherwise user CONNECT & DISCONNECT logics will be breaking.
	claims.Name = string(marshal)

	if data.GetName() == config.RecorderUserAuthName {
		s.setPermissionForRecorder(data, claims)
		return claims, nil
	}

	if err := s.setPermissionForClient(data, claims); err != nil {
		return nil, err
	}

	return claims, nil
}

func (s *NatsAuthController) setPermissionForRecorder(data *plugnmeet.PlugNmeetTokenClaims, claims *jwt.UserClaims) {
	pubAllow := jwt.StringList{
		"$JS.API.INFO",
		"_INBOX.>", // otherwise won't be able to send respond msg
		fmt.Sprintf("$JS.API.STREAM.INFO.KV_%s", s.app.NatsInfo.Recorder.RecorderInfoKv),
		fmt.Sprintf("$KV.%s.>", s.app.NatsInfo.Recorder.RecorderInfoKv),
		fmt.Sprintf("$JS.API.DIRECT.GET.KV_%s.>", s.app.NatsInfo.Recorder.RecorderInfoKv),
		// Allow publishing the job to the stream (variant specific subjects)
		fmt.Sprintf("%s.*", s.app.NatsInfo.Recorder.TranscodingJobs),
		// Allow fetching the next message from the variant consumer & send ack
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.*", s.app.NatsInfo.Recorder.TranscodingJobs),
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.*", s.app.NatsInfo.Recorder.TranscodingJobs),
		fmt.Sprintf("$JS.ACK.%s.>", s.app.NatsInfo.Recorder.TranscodingJobs),
	}

	claims.Permissions = jwt.Permissions{
		Pub: jwt.Permission{
			Allow: pubAllow,
		},
		Sub: jwt.Permission{
			Allow: jwt.StringList{
				s.app.NatsInfo.Recorder.RecorderChannel,
				fmt.Sprintf("%srecorder.%s.>", nats.InboxPrefix, data.GetUserId()),
			},
		},
	}
}

func (s *NatsAuthController) setPermissionForClient(data *plugnmeet.PlugNmeetTokenClaims, claims *jwt.UserClaims) error {
	roomId := data.GetRoomId()
	userId := data.GetUserId()

	if s.natsService.IsUserExistInBlockList(roomId, userId) {
		return config.ErrAuthUserBlocked
	}

	userInfo, err := s.natsService.GetUserInfo(roomId, userId)
	if err != nil {
		s.logUserFailure(roomId, userId, "failed to fetch user info", err)
		return config.ErrAuthUnexpected
	}
	if userInfo == nil {
		return config.ErrAuthUserInfoNotFound
	}

	status, err := s.natsService.GetRoomUserStatus(roomId, userId)
	if err != nil {
		s.logUserFailure(roomId, userId, "failed to fetch room user status", err)
		return config.ErrAuthUnexpected
	}
	if status == natsservice.UserStatusRemoved || status == "" {
		return config.ErrAuthUserNotActive
	}

	meta, err := s.natsService.UnmarshalUserMetadata(userInfo.Metadata)
	if err != nil {
		s.logUserFailure(roomId, userId, "failed to unmarshal user metadata", err)
		return config.ErrAuthUnexpected
	}
	waitForApproval := meta.GetWaitForApproval()

	stream, durableName, err := s.natsService.CreateUserConsumer(roomId, userId, waitForApproval)
	if err != nil {
		s.logUserFailure(roomId, userId, "failed to create user consumer", err)
		return config.ErrAuthUnexpected
	}

	pubAllow := jwt.StringList{
		"$JS.API.INFO",
		// permission for consumer (JetStream)
		fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s", stream, durableName),
		fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s", stream, durableName),
		fmt.Sprintf("$JS.ACK.%s.%s.>", stream, durableName),

		// permission to publish messages to the system (JetStream)
		fmt.Sprintf("%s.%s.%s", s.app.NatsInfo.Subjects.SystemJsWorker, roomId, userId),
		// permission to publish messages to the system (core pub/sub)
		fmt.Sprintf("%s.%s.%s", s.app.NatsInfo.Subjects.SystemCoreWorker, roomId, userId),
	}

	subAllow := jwt.StringList{
		fmt.Sprintf("%s%s.%s.>", nats.InboxPrefix, roomId, userId),
	}

	// a user waiting for approval can't participate in the room channels
	if !waitForApproval {
		pubAllow = append(pubAllow,
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Chat, roomId),
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Whiteboard, roomId),
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.DataChannel, roomId),
		)
		subAllow = append(subAllow,
			// allow to subscribe in pub/sub channel system public which is different from JetStream
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.SystemPublic, roomId),
			// other pub/sub channels
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Chat, roomId),
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.Whiteboard, roomId),
			fmt.Sprintf("%s.%s", s.app.NatsInfo.Subjects.DataChannel, roomId),
		)
	}

	claims.Permissions = jwt.Permissions{
		Pub: jwt.Permission{Allow: pubAllow},
		Sub: jwt.Permission{Allow: subAllow},
	}

	return nil
}

// logUserFailure logs an operational auth failure once, at the failure site.
func (s *NatsAuthController) logUserFailure(roomId, userId, msg string, err error) {
	s.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
	}).WithError(err).Error(msg)
}

// respond answers the authorization request. authErr must be one of the
// config.ErrAuth* key sentinels, so the client always receives a translation key.
func (s *NatsAuthController) respond(req micro.Request, userNKey, serverId, userJWT string, authErr error) {
	rc := jwt.NewAuthorizationResponseClaims(userNKey)
	rc.Audience = serverId
	rc.Jwt = userJWT
	if authErr != nil {
		rc.Error = authErr.Error()
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
