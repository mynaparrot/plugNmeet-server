package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

type NatsController struct {
	ctx       context.Context
	app       *config.AppConfig
	kp        nkeys.KeyPair
	authModel *models.AuthModel
	natsModel *models.NatsModel
}

func NewNatsController() *NatsController {
	app := config.GetConfig()

	kp, err := nkeys.FromSeed([]byte(app.NatsInfo.AuthCalloutIssuerPrivate))
	if err != nil {
		log.Fatal(err)
	}

	ds := dbservice.New(app.DB)
	rs := redisservice.New(app.RDS)

	rm := models.NewAuthModel(app, nil)
	return &NatsController{
		ctx:       context.Background(),
		app:       app,
		kp:        kp,
		authModel: rm,
		natsModel: models.NewNatsModel(app, ds, rs),
	}
}

func (c *NatsController) StartUp() {
	go c.subscribeToUsersConnEvents()

	// system receiver as worker
	_, err := c.app.JetStream.CreateOrUpdateStream(c.ctx, jetstream.StreamConfig{
		Name:      fmt.Sprintf("%s", c.app.NatsInfo.Subjects.SystemJsWorker),
		Retention: jetstream.WorkQueuePolicy, // to become a worker
		Subjects: []string{
			fmt.Sprintf("%s.*.*", c.app.NatsInfo.Subjects.SystemJsWorker),
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// now subscribe
	go c.subscribeToSystemWorker()

	// auth service
	authService := NewNatsAuthController(c.app, c.authModel, c.kp, c.app.JetStream)
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
			go func(data []byte) {
				e := new(NatsEvents)
				if err := json.Unmarshal(data, e); err == nil {
					p := strings.Split(e.Client["user"].(string), ":")
					if len(p) == 2 {
						c.natsModel.OnAfterUserJoined(p[0], p[1])
					}
				}
			}(msg.Data)
		} else if strings.Contains(msg.Subject, ".DISCONNECT") {
			go func(data []byte) {
				e := new(NatsEvents)
				if err := json.Unmarshal(data, e); err == nil {
					p := strings.Split(e.Client["user"].(string), ":")
					if len(p) == 2 {
						c.natsModel.OnAfterUserDisconnected(p[0], p[1])
					}
				}
			}(msg.Data)
		}
	})
	if err != nil {
		log.Fatal(err)
		return
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func (c *NatsController) subscribeToSystemWorker() {
	ip := c.getOutboundIP()

	cons, err := c.app.JetStream.CreateOrUpdateConsumer(c.ctx, fmt.Sprintf("%s", c.app.NatsInfo.Subjects.SystemJsWorker), jetstream.ConsumerConfig{
		Name: strings.ReplaceAll(ip.String(), ".", ":"),
		FilterSubjects: []string{
			fmt.Sprintf("%s.*.*", c.app.NatsInfo.Subjects.SystemJsWorker),
		},
	})
	if err != nil {
		log.Fatalln(err)
		return
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		defer msg.Ack()
		go func(sub string, data []byte) {
			req := new(plugnmeet.NatsMsgClientToServer)
			if err := proto.Unmarshal(data, req); err == nil {
				p := strings.Split(sub, ".")
				roomId := p[1]
				userId := p[2]
				c.natsModel.HandleFromClientToServerReq(roomId, userId, req)
			}
		}(msg.Subject(), msg.Data())
	}, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		log.Errorln(err)
	}))

	if err != nil {
		log.Fatal(err)
		return
	}
	defer cc.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func (c *NatsController) getOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP
}
