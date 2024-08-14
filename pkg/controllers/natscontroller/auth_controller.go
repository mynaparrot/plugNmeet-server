package natscontroller

import (
	"context"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roommodel"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	log "github.com/sirupsen/logrus"
)

type NatsAuthController struct {
	ctx           context.Context
	app           *config.AppConfig
	rm            *roommodel.RoomModel
	js            jetstream.JetStream
	issuerKeyPair nkeys.KeyPair
}

func NewNatsAuthController(app *config.AppConfig, rm *roommodel.RoomModel, kp nkeys.KeyPair, js jetstream.JetStream) *NatsAuthController {
	return &NatsAuthController{
		ctx:           context.Background(),
		app:           app,
		rm:            rm,
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
	fmt.Println(req.ConnectOptions.Token)

	// check the info first
	data, err := s.rm.VerifyPlugNmeetAccessToken(req.ConnectOptions.Token)
	if err != nil {
		return nil, err
	}

	// TODO: now check if user's info exists

	roomId := data.GetRoomId()
	userId := data.GetUserId()
	fmt.Println(roomId)

	// public chat
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.ChatPublic, userId),
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.>", roomId, s.app.NatsInfo.Subjects.ChatPublic),
		},
	})
	if err != nil {
		return nil, err
	}

	// private chat
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.ChatPrivate, userId),
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.%s.>", roomId, s.app.NatsInfo.Subjects.ChatPrivate, userId),
		},
	})
	if err != nil {
		return nil, err
	}

	// system public
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPublic, userId),
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.>", roomId, s.app.NatsInfo.Subjects.SystemPublic),
		},
	})
	if err != nil {
		return nil, err
	}

	// system private
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.SystemPrivate, userId),
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.%s.>", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),
		},
	})
	if err != nil {
		return nil, err
	}

	// whiteboard
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, roomId, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s:%s", s.app.NatsInfo.Subjects.Whiteboard, userId),
		FilterSubjects: []string{
			fmt.Sprintf("%s:%s.>", roomId, s.app.NatsInfo.Subjects.Whiteboard),
		},
	})
	if err != nil {
		return nil, err
	}

	// Assign Permissions
	claims.Name = fmt.Sprintf("%s:%s", roomId, userId)
	claims.Permissions = jwt.Permissions{
		Pub: jwt.Permission{
			Allow: jwt.StringList{
				"$JS.API.INFO",
				fmt.Sprintf("$JS.API.STREAM.INFO.%s", roomId),

				// public message
				fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.ChatPublic, userId),
				fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.ChatPublic, userId),
				fmt.Sprintf("%s:%s.%s", roomId, s.app.NatsInfo.Subjects.ChatPublic, userId),
				fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.ChatPublic, userId),

				// private message
				fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.ChatPrivate, userId),
				fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.ChatPrivate, userId),
				fmt.Sprintf("%s:%s.*.%s", roomId, s.app.NatsInfo.Subjects.ChatPrivate, userId),
				fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.ChatPrivate, userId),

				// system public message
				fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),
				fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),
				fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.SystemPublic, userId),

				// system private message
				fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
				fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
				fmt.Sprintf("%s:%s.*.%s", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),
				fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.SystemPrivate, userId),

				// whiteboard message
				fmt.Sprintf("$JS.API.CONSUMER.INFO.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.Whiteboard, userId),
				fmt.Sprintf("$JS.API.CONSUMER.MSG.NEXT.%s.%s:%s", roomId, s.app.NatsInfo.Subjects.Whiteboard, userId),
				fmt.Sprintf("$JS.ACK.%s.%s:%s.>", roomId, s.app.NatsInfo.Subjects.Whiteboard, userId),

				// allow sending messages to the system
				fmt.Sprintf("%s.%s.%s", s.app.NatsInfo.Subjects.SystemWorker, roomId, userId),
			},
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
