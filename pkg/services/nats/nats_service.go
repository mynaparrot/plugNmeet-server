package natsservice

import (
	"context"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	Prefix = "pnm-"
)

var protoJsonOpts = protojson.MarshalOptions{
	EmitUnpopulated: true,
	UseProtoNames:   true,
}

type NatsService struct {
	ctx    context.Context
	app    *config.AppConfig
	nc     *nats.Conn
	js     jetstream.JetStream
	cs     *NatsCacheService
	logger *logrus.Entry
}

func New(ctx context.Context, app *config.AppConfig, logger *logrus.Logger) *NatsService {
	log := logger.WithField("service", "nats")

	return &NatsService{
		ctx:    ctx,
		app:    app,
		nc:     app.NatsConn,
		js:     app.JetStream,
		cs:     GetNatsCacheService(app, log.Logger),
		logger: log,
	}
}

// MarshalToProtoJson will convert data into proper format
func (s *NatsService) MarshalToProtoJson(m proto.Message) (string, error) {
	marshal, err := protoJsonOpts.Marshal(m)
	if err != nil {
		return "", err
	}

	return string(marshal), nil
}

// MarshalRoomMetadata will convert metadata struct to proper json format
func (s *NatsService) MarshalRoomMetadata(meta *plugnmeet.RoomMetadata) (string, error) {
	mId := uuid.NewString()
	meta.MetadataId = &mId

	marshal, err := s.MarshalToProtoJson(meta)
	if err != nil {
		return "", err
	}

	return marshal, nil
}

// UnmarshalRoomMetadata will convert metadata string to proper format
func (s *NatsService) UnmarshalRoomMetadata(metadata string) (*plugnmeet.RoomMetadata, error) {
	meta := new(plugnmeet.RoomMetadata)
	err := protojson.Unmarshal([]byte(metadata), meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

// MarshalUserMetadata will create proper json string of user's metadata
func (s *NatsService) MarshalUserMetadata(meta *plugnmeet.UserMetadata) (string, error) {
	mId := uuid.NewString()
	meta.MetadataId = &mId

	marshal, err := s.MarshalToProtoJson(meta)
	if err != nil {
		return "", err
	}

	return marshal, nil
}

// UnmarshalUserMetadata will create proper formatted medata from json string
func (s *NatsService) UnmarshalUserMetadata(metadata string) (*plugnmeet.UserMetadata, error) {
	m := new(plugnmeet.UserMetadata)
	err := protojson.Unmarshal([]byte(metadata), m)
	if err != nil {
		return nil, err
	}

	return m, nil
}
