package controllers

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

const aiAgentTestRoomId = "room-1"

// offlineNatsJetStream stands in for the JetStream client, so the model gets a
// real NATS service without a NATS server: no bucket exists, so no room is found.
type offlineNatsJetStream struct{ jetstream.JetStream }

func (offlineNatsJetStream) KeyValue(context.Context, string) (jetstream.KeyValue, error) {
	return nil, jetstream.ErrBucketNotFound
}

func (offlineNatsJetStream) Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	return &jetstream.PubAck{}, nil
}

// newAIAgentControllerTestModel returns a controller whose session-control
// lookup runs against an in-memory Redis and a JetStream stand-in, so the tests
// need no NATS server.
func newAIAgentControllerTestModel(t *testing.T) (*AIAgentController, redis.UniversalClient) {
	t.Helper()

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	logger := logrus.New()
	ttl := 2 * time.Hour
	app := &config.AppConfig{NatsInfo: config.NatsInfo{DefaultTTL: &ttl}}

	model := models.NewAIAgentModel(models.AIAgentModelArgs{
		Ctx:         context.Background(),
		AppConfig:   app,
		RDS:         rc,
		NatsService: natsservice.New(natsservice.Args{Ctx: context.Background(), App: app, Js: offlineNatsJetStream{}, Logger: logger}),
		Logger:      logger,
	})

	return &AIAgentController{
		aiAgentModel: model,
		logger:       logger.WithField("controller", "aiAgent"),
	}, rc
}

func decodeAIAgentStatus(t *testing.T, resp *http.Response) *plugnmeet.AIAgentStatusRes {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	res := new(plugnmeet.AIAgentStatusRes)
	if err = proto.Unmarshal(body, res); err != nil {
		t.Fatalf("failed to decode AIAgentStatusRes: %v", err)
	}

	return res
}

// makeDelegate makes userId the room's current agent delegate, which is what
// allows a participant to control the session.
func makeDelegate(t *testing.T, rc redis.UniversalClient, userId string) {
	t.Helper()

	key := "pnm:ai_agent:" + aiAgentTestRoomId + ":delegate"
	if err := rc.Set(context.Background(), key, userId, 0).Err(); err != nil {
		t.Fatalf("failed to store the delegate: %v", err)
	}
}

func assertAIAgentDenied(t *testing.T, res *plugnmeet.AIAgentStatusRes) {
	t.Helper()

	if res.GetStatus() || res.GetMsg() != config.ErrAIAgentNotAllowed.Error() {
		t.Errorf("response = %v/%q, want the %q denial", res.GetStatus(), res.GetMsg(), config.ErrAIAgentNotAllowed.Error())
	}
}

func TestAIAgentControllerStartStopDeniesOtherParticipants(t *testing.T) {
	ac, _ := newAIAgentControllerTestModel(t)
	locals := roomTestLocals{roomId: aiAgentTestRoomId, requestedUserId: "student-2"}

	t.Run("start", func(t *testing.T) {
		app := newRoomTestApp(ac.HandleStart, locals)
		assertAIAgentDenied(t, decodeAIAgentStatus(t, sendRoomTestRequest(t, app, "")))
	})

	t.Run("stop", func(t *testing.T) {
		app := newRoomTestApp(ac.HandleStop, locals)
		assertAIAgentDenied(t, decodeAIAgentStatus(t, sendRoomTestRequest(t, app, "")))
	})
}

func TestAIAgentControllerStopReasons(t *testing.T) {
	ac, rc := newAIAgentControllerTestModel(t)
	makeDelegate(t, rc, "student-1")

	t.Run("the delegate stops the session", func(t *testing.T) {
		app := newRoomTestApp(ac.HandleStop, roomTestLocals{roomId: aiAgentTestRoomId, requestedUserId: "student-1"})
		res := decodeAIAgentStatus(t, sendRoomTestRequest(t, app, ""))

		if !res.GetStatus() || res.GetState().GetReason() != "user_stopped" {
			t.Errorf("stop = %v/%q, want a user_stopped session", res.GetStatus(), res.GetState().GetReason())
		}
	})

	t.Run("an admin stops the session", func(t *testing.T) {
		app := newRoomTestApp(ac.HandleStop, roomTestLocals{
			roomId:          aiAgentTestRoomId,
			requestedUserId: "admin-1",
			isAdmin:         true,
		})
		res := decodeAIAgentStatus(t, sendRoomTestRequest(t, app, ""))

		if !res.GetStatus() || res.GetState().GetReason() != "host_stopped" {
			t.Errorf("stop = %v/%q, want a host_stopped session", res.GetStatus(), res.GetState().GetReason())
		}
	})
}
