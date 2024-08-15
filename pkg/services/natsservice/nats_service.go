package natsservice

import (
	"context"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
)

type NatsService struct {
	ctx context.Context
	app *config.AppConfig
	rs  *redisservice.RedisService
	nc  *nats.Conn
	js  jetstream.JetStream
}

func New(app *config.AppConfig, rs *redisservice.RedisService) *NatsService {
	return &NatsService{
		ctx: context.Background(),
		app: app,
		rs:  rs,
		nc:  app.NatsConn,
		js:  app.JetStream,
	}
}

// MarshalRoomMetadata will convert metadata struct to proper json format
func (s *NatsService) MarshalRoomMetadata(meta *plugnmeet.RoomMetadata) (string, error) {
	mId := uuid.NewString()
	meta.MetadataId = &mId

	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}

	marshal, err := op.Marshal(meta)
	if err != nil {
		return "", err
	}

	return string(marshal), nil
}
