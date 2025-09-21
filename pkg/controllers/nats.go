package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

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
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultNumWorkers Number of concurrent workers for processing NATS messages.
	DefaultNumWorkers = 50
	// DefaultJobQueueSize Size of the job queue. A larger buffer can handle larger bursts of messages.
	DefaultJobQueueSize = 1000
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

type natsJob struct {
	handler func()
}

type NatsController struct {
	app           *config.AppConfig
	natsService   *natsservice.NatsService
	issuerKeyPair nkeys.KeyPair
	curveKeyPair  nkeys.KeyPair
	authModel     *models.AuthModel
	natsModel     *models.NatsModel
	jobChan       chan natsJob
	logger        *logrus.Entry
}

func NewNatsController(app *config.AppConfig, natsService *natsservice.NatsService, authModel *models.AuthModel, natsModel *models.NatsModel, logger *logrus.Logger) *NatsController {
	issuerKeyPair, err := nkeys.FromSeed([]byte(app.NatsInfo.AuthCalloutIssuerPrivate))
	if err != nil {
		logger.WithError(err).Fatal("error creating issuer key pair")
	}

	c := &NatsController{
		app:           app,
		natsService:   natsService,
		issuerKeyPair: issuerKeyPair,
		authModel:     authModel,
		natsModel:     natsModel,
		jobChan:       make(chan natsJob, DefaultJobQueueSize),
		logger:        logger.WithField("controller", "nats"),
	}

	if app.NatsInfo.AuthCalloutXkeyPrivate != nil && *app.NatsInfo.AuthCalloutXkeyPrivate != "" {
		c.curveKeyPair, err = nkeys.FromSeed([]byte(*app.NatsInfo.AuthCalloutXkeyPrivate))
		if err != nil {
			c.logger.WithError(err).Fatal("error creating curve key pair")
		}
	}

	return c
}

func (c *NatsController) BootUp(ctx context.Context, wg *sync.WaitGroup) {
	// Start the worker pool
	for i := 0; i < DefaultNumWorkers; i++ {
		go c.worker()
	}

	// system receiver as worker
	stream, err := c.app.JetStream.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      fmt.Sprintf("%s", c.app.NatsInfo.Subjects.SystemJsWorker),
		Replicas:  c.app.NatsInfo.NumReplicas,
		Retention: jetstream.WorkQueuePolicy, // to become a worker
		Subjects: []string{
			fmt.Sprintf("%s.*.*", c.app.NatsInfo.Subjects.SystemJsWorker),
		},
	})
	if err != nil {
		c.logger.WithError(err).Fatal("error creating system worker stream")
	}

	// now subscribe
	sysWorkerCon, err := c.subscribeToSystemWorker(ctx, stream)
	if err != nil {
		c.logger.WithError(err).Fatal("error subscribing to system worker")
	}

	// subscribe to connection events
	con, err := c.subscribeToUsersConnEvents()
	if err != nil {
		c.logger.WithError(err).Fatal("error subscribing to users connection events")
	}

	// auth service
	authService := NewNatsAuthController(c.app, c.natsService, c.authModel, c.issuerKeyPair, c.curveKeyPair, c.logger)
	_, err = micro.AddService(c.app.NatsConn, micro.Config{
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
		c.logger.WithError(err).Fatal("error adding auth service")
	}
	wg.Done()

	// Keep the application running until context remain valid
	<-ctx.Done()

	sysWorkerCon.Stop()
	_ = con.Unsubscribe()
}

func (c *NatsController) worker() {
	for job := range c.jobChan {
		job.handler()
	}
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

		c.jobChan <- natsJob{handler: func() {
			c.handleUserConnectionEvent(data, isConnect)
		}}
	})
}

func (c *NatsController) handleUserConnectionEvent(data []byte, isConnect bool) {
	e := &struct {
		Type   string                 `json:"type"`
		Client map[string]interface{} `json:"client"`
		Reason string                 `json:"reason"`
	}{}
	if err := json.Unmarshal(data, e); err != nil {
		c.logger.WithError(err).Warn("failed to unmarshal NATS connection event")
		return
	}
	log := c.logger.WithFields(logrus.Fields{
		"type":      e.Type,
		"client":    e.Client,
		"reason":    e.Reason,
		"isConnect": isConnect,
	})
	log.Debug("received NATS connection event")

	if clientType, ok := e.Client["client_type"]; ok && clientType != websocketClientType {
		// this feature only for websocket connections from frontend only
		// for other client different ways, so preventing unnecessary errors
		log.WithField("client_type", clientType).Warn("ignoring non-websocket connection event")
		return
	}

	if userToken, ok := e.Client["user"].(string); ok {
		claims, err := c.authModel.UnsafeClaimsWithoutVerification(userToken)
		if err != nil {
			log.WithError(err).Errorln("failed to parse claims from connection event")
			return
		}
		if claims.GetName() != config.RecorderUserAuthName {
			if isConnect {
				c.natsModel.OnAfterUserJoined(claims.GetRoomId(), claims.GetUserId())
			} else {
				c.natsModel.OnAfterUserDisconnected(claims.GetRoomId(), claims.GetUserId())
			}
		}
	}
}

func (c *NatsController) subscribeToSystemWorker(ctx context.Context, stream jetstream.Stream) (jetstream.ConsumeContext, error) {
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s%s", prefix, c.app.NatsInfo.Subjects.SystemJsWorker),
	})
	if err != nil {
		c.logger.WithError(err).Fatalln("error creating system worker consumer")
	}

	consumeContext, err := cons.Consume(func(msg jetstream.Msg) {
		defer msg.Ack()
		// Copy data to avoid race conditions as the message buffer is reused.
		sub := msg.Subject()
		data := make([]byte, len(msg.Data()))
		copy(data, msg.Data())

		c.jobChan <- natsJob{handler: func() {
			req := new(plugnmeet.NatsMsgClientToServer)
			if err := proto.Unmarshal(data, req); err == nil {
				p := strings.Split(sub, ".")
				if len(p) == 3 {
					c.natsModel.HandleFromClientToServerReq(p[1], p[2], req)
				}
			}
		}}
	}, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		if ctx.Err() == nil {
			c.logger.WithError(err).Warn("jetstream consume error")
		}
	}))

	return consumeContext, err
}
