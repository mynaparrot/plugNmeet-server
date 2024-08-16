package natscontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/natsmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roommodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	log "github.com/sirupsen/logrus"
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
	rm        *roommodel.RoomModel
	natsModel *natsmodel.NatsModel
}

func NewNatsController() *NatsController {
	app := config.GetConfig()

	kp, err := nkeys.FromSeed([]byte(app.NatsInfo.AuthCalloutIssuerPrivate))
	if err != nil {
		log.Fatal(err)
	}

	ds := dbservice.New(app.ORM)
	rs := redisservice.New(app.RDS)
	lk := livekitservice.New(app, rs)

	rm := roommodel.New(app, ds, rs, lk)
	return &NatsController{
		ctx:       context.Background(),
		app:       app,
		kp:        kp,
		rm:        rm,
		natsModel: natsmodel.New(app, ds, rs),
	}
}

func (c *NatsController) StartUp() {
	go c.subscribeToUsersConnEvents()

	// system receiver as worker
	_, err := c.app.JetStream.CreateOrUpdateStream(c.ctx, jetstream.StreamConfig{
		Name:      fmt.Sprintf("%s", c.app.NatsInfo.Subjects.SystemWorker),
		Retention: jetstream.WorkQueuePolicy, // to become a worker
		Subjects: []string{
			fmt.Sprintf("%s.*.*", c.app.NatsInfo.Subjects.SystemWorker),
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	// now subscribe
	go c.subscribeToSystemWorker()

	// auth service
	authService := NewNatsAuthController(c.app, c.rm, c.kp, c.app.JetStream)
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

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
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
			e := new(NatsEvents)
			err := json.Unmarshal(msg.Data, e)
			if err != nil {
				return
			}
			p := strings.Split(e.Client["user"].(string), ":")
			err = c.natsModel.OnAfterUserJoined(p[0], p[1])
			if err != nil {
				log.Errorln(err)
			}
		} else if strings.Contains(msg.Subject, ".DISCONNECT") {
			e := new(NatsEvents)
			err := json.Unmarshal(msg.Data, e)
			if err != nil {
				return
			}
			go func() {
				p := strings.Split(e.Client["user"].(string), ":")
				err = c.natsModel.OnAfterUserDisconnected(p[0], p[1])
				if err != nil {
					log.Errorln(err)
				}
			}()
		}
	})
	if err != nil {
		log.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func (c *NatsController) subscribeToSystemWorker() {
	ip := c.getOutboundIP()

	cons, err := c.app.JetStream.CreateOrUpdateConsumer(c.ctx, fmt.Sprintf("%s", c.app.NatsInfo.Subjects.SystemWorker), jetstream.ConsumerConfig{
		Name: strings.ReplaceAll(ip.String(), ".", ":"),
		FilterSubjects: []string{
			fmt.Sprintf("%s.*.*", c.app.NatsInfo.Subjects.SystemWorker),
		},
	})
	if err != nil {
		log.Fatalln(err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		fmt.Println(msg.Subject())

		fmt.Println(string(msg.Data()))
		msg.Ack()

		p := strings.Split(msg.Subject(), ".")
		fmt.Println(p[1])

		sub := fmt.Sprintf("%s:%s.system", p[1], c.app.NatsInfo.Subjects.SystemPublic)
		_, err := c.app.JetStream.Publish(c.ctx, sub, []byte("sending back.."))
		if err != nil {
			log.Errorln(err)
		}

		//// after task reply back
		//sub := fmt.Sprintf("%s:system.jibon", RoomId)
		//_, err := c.app.JetStream.Publish(c.appctx, sub, []byte("sending back.."))
		//if err != nil {
		//	log.Fatal(err)
		//}

	}, jetstream.ConsumeErrHandler(func(consumeCtx jetstream.ConsumeContext, err error) {
		log.Errorln(err)
	}))
	if err != nil {
		log.Fatal(err)
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