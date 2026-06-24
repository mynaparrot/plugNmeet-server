package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gammazero/workerpool"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultNumWorkers Number of concurrent workers for processing NATS messages.
	DefaultNumWorkers = 50
	// nats auth service endpoint subject
	natsAuthServiceEndpointSubject = "$SYS.REQ.USER.AUTH"
	// nats connection event subject format
	natsConnectionEventSubjectFormat = "$SYS.ACCOUNT.%s.>"

	prefix = "pnm-"
	// nats auth service name
	natsAuthServiceName = prefix + "auth"
	// nats auth service queue group
	natsAuthServiceQueueGroup = prefix + "auth-queue"
	// nats connection event queue
	natsConnectionEventQueueGroup = prefix + "conn-event-queue"
	websocketClientType           = "websocket"
)

type NatsController struct {
	ctx              context.Context
	app              *config.AppConfig
	natsService      *natsservice.NatsService
	issuerKeyPair    nkeys.KeyPair
	curveKeyPair     nkeys.KeyPair
	authModel        *models.AuthModel
	natsModel        *models.NatsModel
	wp               *workerpool.WorkerPool
	log              *logrus.Entry
	sysWorkerCon     jetstream.ConsumeContext
	sysWorkerCoreSub *nats.Subscription
	userConnSub      *nats.Subscription
	authService      micro.Service
}

type NatsControllerArgs struct {
	fx.In
	Ctx         context.Context
	App         *config.AppConfig
	NatsService *natsservice.NatsService
	AuthModel   *models.AuthModel
	NatsModel   *models.NatsModel
	Logger      *logrus.Logger
}

func NewNatsController(args NatsControllerArgs) (*NatsController, error) {
	log := args.Logger.WithField("controller", "nats")

	issuerKeyPair, err := nkeys.FromSeed([]byte(args.App.NatsInfo.AuthCalloutIssuerPrivate))
	if err != nil {
		log.WithError(err).Error("Failed to load issuer private key")
		return nil, fmt.Errorf("error creating issuer key pair: %w", err)
	}

	c := &NatsController{
		ctx:           args.Ctx,
		app:           args.App,
		natsService:   args.NatsService,
		issuerKeyPair: issuerKeyPair,
		authModel:     args.AuthModel,
		natsModel:     args.NatsModel,
		wp:            workerpool.New(DefaultNumWorkers),
		log:           log,
	}

	if args.App.NatsInfo.AuthCalloutXkeyPrivate != nil && *args.App.NatsInfo.AuthCalloutXkeyPrivate != "" {
		c.curveKeyPair, err = nkeys.FromSeed([]byte(*args.App.NatsInfo.AuthCalloutXkeyPrivate))
		if err != nil {
			log.WithError(err).Error("Failed to load curve private key")
			return nil, fmt.Errorf("error creating curve key pair: %w", err)
		}
	}

	return c, nil
}

// Initialize performs setup and signals completion via the channel.
func (c *NatsController) Initialize() error {
	log := c.log.WithField("method", "initialize")
	var err error

	// system receiver as worker
	c.sysWorkerCon, err = c.subscribeToSystemWorker(c.ctx)
	if err != nil {
		log.WithError(err).Error("error subscribing to system worker")
		return err
	}
	log.Info("Subscribed to system worker")

	c.sysWorkerCoreSub, err = c.subscribeToSystemWorkerCore()
	if err != nil {
		log.WithError(err).Error("error subscribing to system worker via core NATS")
		return err
	}
	log.Info("Subscribed to system worker via core NATS")

	// create recorder transcoder worker
	if err := c.natsService.CreateTranscoderStreamWithConsumer(c.ctx, log); err != nil {
		return err
	}

	c.userConnSub, err = c.subscribeToUsersConnEvents()
	if err != nil {
		log.WithError(err).Error("error subscribing to users connection events")
		return err
	}
	log.Info("Subscribed to users connection events")

	// auth service
	authService := NewNatsAuthController(c.app, c.natsService, c.authModel, c.issuerKeyPair, c.curveKeyPair, c.log)
	c.authService, err = micro.AddService(c.app.NatsConn, micro.Config{
		Name:        natsAuthServiceName,
		Version:     version.Version,
		Description: "Handle authorization of pnm nats client",
		QueueGroup:  natsAuthServiceQueueGroup,
		Endpoint: &micro.EndpointConfig{
			Subject: natsAuthServiceEndpointSubject,
			Handler: micro.HandlerFunc(authService.Handle),
		},
	})
	if err != nil {
		log.WithError(err).Error("error adding auth service")
		return err
	}

	log.Info("Added auth service")
	return nil
}

// Stop gracefully shuts down the NATS controller's consumers.
func (c *NatsController) Stop() {
	c.log.Info("Shutting down nats controller")
	if c.authService != nil {
		_ = c.authService.Stop()
	}
	if c.sysWorkerCon != nil {
		c.sysWorkerCon.Stop()
	}
	if c.sysWorkerCoreSub != nil {
		_ = c.sysWorkerCoreSub.Unsubscribe()
	}
	if c.userConnSub != nil {
		_ = c.userConnSub.Unsubscribe()
	}
	c.wp.Stop()
}

// SubscribeToUsersConnEvents will be used to subscribe with users' connection events
// based on user connection we can determine user's connection status
func (c *NatsController) subscribeToUsersConnEvents() (*nats.Subscription, error) {
	return c.app.NatsConn.QueueSubscribe(fmt.Sprintf(natsConnectionEventSubjectFormat, c.app.NatsInfo.Account), natsConnectionEventQueueGroup, func(msg *nats.Msg) {
		isConnect := strings.Contains(msg.Subject, ".CONNECT")
		isDisconnect := strings.Contains(msg.Subject, ".DISCONNECT")

		if !isConnect && !isDisconnect {
			return
		}

		// Copy data to avoid race conditions as the message buffer is reused by the NATS client.
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)

		c.wp.Submit(func() {
			c.handleUserConnectionEvent(data, isConnect)
		})
	})
}

func (c *NatsController) handleUserConnectionEvent(data []byte, isConnect bool) {
	e := &struct {
		Type   string                 `json:"type"`
		Client map[string]interface{} `json:"client"`
		Reason string                 `json:"reason"`
	}{}
	if err := json.Unmarshal(data, e); err != nil {
		c.log.WithError(err).Warn("failed to unmarshal NATS connection event")
		return
	}
	log := c.log.WithFields(logrus.Fields{
		"type":      e.Type,
		"client":    e.Client,
		"reason":    e.Reason,
		"isConnect": isConnect,
	})
	log.Debug("Received NATS connection event")

	if clientType, ok := e.Client["client_type"]; ok && clientType != websocketClientType {
		// this feature only for websocket connections from frontend only
		// for other client different ways, so preventing unnecessary errors
		log.WithField("client_type", clientType).Warn("ignoring non-websocket connection event")
		return
	}

	if user, ok := e.Client["user"].(string); ok {
		claims := new(plugnmeet.PlugNmeetTokenClaims)
		if err := protojson.Unmarshal([]byte(user), claims); err != nil {
			log.WithError(err).Warn("failed to unmarshal user claims")
			return
		}
		if claims.GetName() != config.RecorderUserAuthName {
			if isConnect {
				c.natsModel.OnAfterUserJoined(claims.GetRoomId(), claims.GetUserId(), "handleUserConnectionEvent")
			} else {
				c.natsModel.OnAfterUserDisconnected(claims.GetRoomId(), claims.GetUserId(), "handleUserConnectionEvent")
			}
		}
	}
}

// subscribeToSystemWorkerCore subscribes to the SystemCoreWorker subject via core NATS pub/sub.
// This is intended for lightweight, fire-and-forget messages (e.g., analytics) that don't require JetStream's guarantees.
func (c *NatsController) subscribeToSystemWorkerCore() (*nats.Subscription, error) {
	subject := fmt.Sprintf("%s.*.*", c.app.NatsInfo.Subjects.SystemCoreWorker)
	// Use a queue group to load-balance across multiple server instances.
	queue := fmt.Sprintf("%s%s", prefix, c.app.NatsInfo.Subjects.SystemCoreWorker)

	return c.app.NatsConn.QueueSubscribe(subject, queue, func(msg *nats.Msg) {
		// Copy data to avoid race conditions as the message buffer is reused.
		sub := msg.Subject
		data := make([]byte, len(msg.Data))
		copy(data, msg.Data)

		c.wp.Submit(func() {
			req := new(plugnmeet.NatsMsgClientToServer)
			if err := proto.Unmarshal(data, req); err == nil {
				p := strings.Split(sub, ".")
				if len(p) == 3 {
					// The handler is the same as the JetStream one.
					// The natsModel will differentiate the message by its event type.
					c.natsModel.HandleFromClientToServerReq(p[1], p[2], req)
				}
			}
		})
	})
}

// subscribeToSystemWorker subscribes to the system worker subject via JetStream.
// This is used for messages that require guaranteed delivery, such as PINGs, token renewals, and private messages.
// It runs in parallel with the core NATS pub/sub subscriber.
func (c *NatsController) subscribeToSystemWorker(ctx context.Context) (jetstream.ConsumeContext, error) {
	log := c.log.WithField("method", "subscribeToSystemWorker")
	cons, err := c.natsService.CreateSystemJsWorkerStreamWithConsumer(ctx, prefix, log)
	if err != nil {
		return nil, err
	}

	consumeContext, err := cons.Consume(func(msg jetstream.Msg) {
		defer msg.Ack()
		// Copy data to avoid race conditions as the message buffer is reused.
		sub := msg.Subject()
		data := make([]byte, len(msg.Data()))
		copy(data, msg.Data())

		c.wp.Submit(func() {
			req := new(plugnmeet.NatsMsgClientToServer)
			if err := proto.Unmarshal(data, req); err == nil {
				p := strings.Split(sub, ".")
				if len(p) == 3 {
					c.natsModel.HandleFromClientToServerReq(p[1], p[2], req)
				}
			}
		})
	}, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		if ctx.Err() == nil {
			if !errors.Is(err, jetstream.ErrConnectionClosed) {
				log.WithError(err).Warn("jetstream consume error")
			}
		}
	}))

	return consumeContext, err
}
