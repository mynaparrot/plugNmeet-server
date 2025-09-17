package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
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
	// Adjust based on expected load and available resources.
	DefaultNumWorkers = 50
	// DefaultJobQueueSize Size of the job queue. A larger buffer can handle larger bursts of messages.
	DefaultJobQueueSize = 1000
)

type natsJob struct {
	handler func()
}

type NatsController struct {
	ctx           context.Context
	app           *config.AppConfig
	issuerKeyPair nkeys.KeyPair
	curveKeyPair  nkeys.KeyPair
	authModel     *models.AuthModel
	natsModel     *models.NatsModel
	jobChan       chan natsJob
	logger        *logrus.Entry
}

func NewNatsController(app *config.AppConfig, authModel *models.AuthModel, natsModel *models.NatsModel, logger *logrus.Logger) *NatsController {
	issuerKeyPair, err := nkeys.FromSeed([]byte(app.NatsInfo.AuthCalloutIssuerPrivate))
	if err != nil {
		logger.WithError(err).Fatal("error creating issuer key pair")
	}

	c := &NatsController{
		ctx:           context.Background(),
		app:           app,
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

func (c *NatsController) BootUp(wg *sync.WaitGroup) {
	// Start the worker pool
	for i := 0; i < DefaultNumWorkers; i++ {
		go c.worker()
	}

	// system receiver as worker
	stream, err := c.app.JetStream.CreateOrUpdateStream(c.ctx, jetstream.StreamConfig{
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
	c.subscribeToSystemWorker(stream)
	// subscribe to connection events
	c.subscribeToUsersConnEvents()

	// auth service
	authService := NewNatsAuthController(c.app, c.authModel, c.issuerKeyPair, c.curveKeyPair, c.logger.Logger)
	_, err = micro.AddService(c.app.NatsConn, micro.Config{
		Name:        "pnm-auth",
		Version:     version.Version,
		Description: "Handle authorization of pnm nats client",
		QueueGroup:  "pnm-auth",
		Endpoint: &micro.EndpointConfig{
			Subject: "$SYS.REQ.USER.AUTH",
			Handler: micro.HandlerFunc(authService.Handle),
		},
	})

	if err != nil {
		c.logger.WithError(err).Fatal("error adding auth service")
	}
	wg.Done()

	// Keep the application running until a signal is received.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func (c *NatsController) worker() {
	for job := range c.jobChan {
		job.handler()
	}
}

type NatsEvents struct {
	Client map[string]interface{} `json:"client"`
	Reason string                 `json:"reason"`
}

// SubscribeToUsersConnEvents will be used to subscribe with users' connection events
// based on user connection we can determine user's connection status
func (c *NatsController) subscribeToUsersConnEvents() {
	_, err := c.app.NatsConn.QueueSubscribe(fmt.Sprintf("$SYS.ACCOUNT.%s.>", c.app.NatsInfo.Account), "pnm-conn-event", func(msg *nats.Msg) {
		if strings.Contains(msg.Subject, ".CONNECT") {
			// Copy data only when we need it to avoid unnecessary allocations.
			// This is crucial to prevent race conditions as the message buffer is reused by the NATS client.
			data := make([]byte, len(msg.Data))
			copy(data, msg.Data)

			c.jobChan <- natsJob{handler: func() {
				e := new(NatsEvents)
				if err := json.Unmarshal(data, e); err == nil {
					if user, ok := e.Client["user"].(string); ok {
						claims, err := c.authModel.UnsafeClaimsWithoutVerification(user)
						if err != nil {
							c.logger.WithError(err).Errorln("failed to parse claims from connect event")
							return
						}
						if claims.GetName() != config.RecorderUserAuthName {
							c.natsModel.OnAfterUserJoined(claims.GetRoomId(), claims.GetUserId())
						}
					}
				}
			}}
		} else if strings.Contains(msg.Subject, ".DISCONNECT") {
			// Copy data only when we need it.
			data := make([]byte, len(msg.Data))
			copy(data, msg.Data)

			c.jobChan <- natsJob{handler: func() {
				e := new(NatsEvents)
				if err := json.Unmarshal(data, e); err == nil {
					if user, ok := e.Client["user"].(string); ok {
						claims, err := c.authModel.UnsafeClaimsWithoutVerification(user)
						if err != nil {
							c.logger.WithError(err).Errorln("failed to parse claims from disconnect event")
							return
						}
						if claims.GetName() != config.RecorderUserAuthName {
							c.natsModel.OnAfterUserDisconnected(claims.GetRoomId(), claims.GetUserId())
						}
					}
				}
			}}
		}
	})
	if err != nil {
		c.logger.WithError(err).Fatal("error subscribing to users connection events")
		return
	}
}

func (c *NatsController) subscribeToSystemWorker(stream jetstream.Stream) {
	cons, err := stream.CreateOrUpdateConsumer(c.ctx, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("pnm-%s", c.app.NatsInfo.Subjects.SystemJsWorker),
	})
	if err != nil {
		c.logger.WithError(err).Fatalln("error creating system worker consumer")
	}

	_, err = cons.Consume(func(msg jetstream.Msg) {
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
		c.logger.WithError(err).Errorf("jetstream consume error")
	}))

	if err != nil {
		c.logger.WithError(err).Fatal("error subscribing to system worker")
		return
	}
}
