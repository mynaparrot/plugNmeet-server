package natsservice

import (
	"context"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	Prefix = "pnm-"
)

//var mustCompile = regexp.MustCompile("/[_.:]/g")

type NatsService struct {
	ctx context.Context
	app *config.AppConfig
	nc  *nats.Conn
	js  jetstream.JetStream
}

func New(app *config.AppConfig) *NatsService {
	if app == nil {
		app = config.GetConfig()
	}
	return &NatsService{
		ctx: context.Background(),
		app: app,
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

// MarshalParticipantMetadata will create proper json string of user's metadata
func (s *NatsService) MarshalParticipantMetadata(meta *plugnmeet.UserMetadata) (string, error) {
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

// UnmarshalParticipantMetadata will create proper formatted medata from json string
func (s *NatsService) UnmarshalParticipantMetadata(metadata string) (*plugnmeet.UserMetadata, error) {
	m := new(plugnmeet.UserMetadata)
	err := protojson.Unmarshal([]byte(metadata), m)
	if err != nil {
		return nil, err
	}

	return m, nil
}

// MarshalToProtoJson will convert data into proper format
func (s *NatsService) MarshalToProtoJson(m proto.Message) (string, error) {
	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}

	marshal, err := op.Marshal(m)
	if err != nil {
		return "", err
	}

	return string(marshal), nil
}
