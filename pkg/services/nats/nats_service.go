package natsservice

import (
	"context"
	"time"

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
	Prefix     = "pnm-"
	DefaultTTL = time.Hour * 24

	// ConsolidatedRoomBucketPrefix is the prefix for the new single bucket for all room-related data.
	ConsolidatedRoomBucketPrefix = Prefix + "room-"

	// RoomInfoKeyPrefix format: info_<field>
	RoomInfoKeyPrefix = "info_"

	// UserKeyPrefix format: user_<userId>-FIELD_<field>
	UserKeyPrefix = "user_"
	// UserKeyFieldPrefix is the separator between the userId and the field.
	UserKeyFieldPrefix = "-FIELD_"
	// FileKeyPrefix format: file_<fileId>
	FileKeyPrefix = "file_"
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
	cs := newNatsCacheService(ctx, log)

	return &NatsService{
		ctx:    ctx,
		app:    app,
		nc:     app.NatsConn,
		js:     app.JetStream,
		cs:     cs,
		logger: log,
	}
}

// formatConsolidatedRoomBucket generates the bucket name for a consolidated room.
// The format will be `pnm-room-<roomId>`.
func (s *NatsService) formatConsolidatedRoomBucket(roomId string) string {
	return ConsolidatedRoomBucketPrefix + roomId
}

// formatRoomKey generates a key for a room-level info field.
// The format will be `info_<field>`.
func (s *NatsService) formatRoomKey(field string) string {
	return RoomInfoKeyPrefix + field
}

// formatUserKey generates a key for a specific user's field.
// The format will be `user_<userId>-FIELD_<field>`.
func (s *NatsService) formatUserKey(userId, field string) string {
	return UserKeyPrefix + userId + UserKeyFieldPrefix + field
}

// formatFileKey generates the key for a specific file's metadata.
// The format will be `file_<fileId>`.
func (s *NatsService) formatFileKey(fileId string) string {
	return FileKeyPrefix + fileId
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
