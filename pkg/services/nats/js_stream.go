package natsservice

import (
	"context"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
)

const (
	DurableNameTpl = "%s_%s"

	// we'll try maximum of 3 times, we've same the value in recorder as well
	maxTranscodingRetries = 3
	// in transcoder we've msg.InProgress() update loop but still we can set time little bit longer
	maxTranscodingAckWait = time.Minute * 10
)

func (s *NatsService) CreateSystemJsWorkerStreamWithConsumer(ctx context.Context, prefix string, log *logrus.Entry) (jetstream.Consumer, error) {
	stream, err := s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        s.app.NatsInfo.Subjects.SystemJsWorker,
		Description: "plugNmeet system worker",
		Replicas:    s.app.NatsInfo.NumReplicas,
		Retention:   jetstream.WorkQueuePolicy,
		Subjects: []string{
			fmt.Sprintf("%s.*.*", s.app.NatsInfo.Subjects.SystemJsWorker),
		},
	})
	if err != nil {
		log.WithError(err).Error("error creating system worker stream")
		return nil, err
	}
	log.Info("Created/Updated system worker stream")

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable: fmt.Sprintf("%s%s", prefix, s.app.NatsInfo.Subjects.SystemJsWorker),
	})
	if err != nil {
		log.WithError(err).Error("error creating system worker consumer")
		return nil, err
	}
	log.Info("Created/Updated system worker consumer")

	return cons, nil
}

// createRoomNatsStream will create a single stream for all rooms.
func (s *NatsService) createRoomNatsStream() error {
	_, err := s.js.CreateOrUpdateStream(s.ctx, jetstream.StreamConfig{
		Name:        s.app.NatsInfo.RoomStreamName,
		Description: "plugNmeet room stream",
		Replicas:    s.app.NatsInfo.NumReplicas,
		Retention:   jetstream.InterestPolicy,
		Subjects: []string{
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.SystemPublic),
			fmt.Sprintf("%s.>", s.app.NatsInfo.Subjects.SystemPrivate),
		},
	})
	if err != nil {
		s.logger.WithError(err).Errorf("error creating room stream: %s", s.app.NatsInfo.RoomStreamName)
		return err
	}
	s.logger.Infof("Successfully created/updated room stream: %s", s.app.NatsInfo.RoomStreamName)
	return nil
}

// PurgeRoomMessagesFromStream purges all message subjects for a specific room from the main stream.
// It does NOT delete the stream itself.
func (s *NatsService) PurgeRoomMessagesFromStream(roomId string) error {
	// Purge all subjects for the specific room.
	stream, err := s.js.Stream(s.ctx, s.app.NatsInfo.RoomStreamName)
	if err != nil {
		return err
	}
	return stream.Purge(s.ctx,
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPublic, roomId)),
		jetstream.WithPurgeSubject(fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPrivate, roomId)),
	)
}

// CreateUserConsumer creates a single consumer per user for chat, public, and private system messages.
func (s *NatsService) CreateUserConsumer(roomId, userId string) (stream string, durableName string, err error) {
	durableName = fmt.Sprintf(DurableNameTpl, roomId, userId)
	_, err = s.js.CreateOrUpdateConsumer(s.ctx, s.app.NatsInfo.RoomStreamName, jetstream.ConsumerConfig{
		Durable:       durableName,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		FilterSubjects: []string{
			// e.g., "sysPublic.room123.>"
			fmt.Sprintf("%s.%s.>", s.app.NatsInfo.Subjects.SystemPublic, roomId),
			// e.g., "sysPrivate.room123.user456.>"
			fmt.Sprintf("%s.%s.%s.>", s.app.NatsInfo.Subjects.SystemPrivate, roomId, userId),
		},
	})
	if err != nil {
		return "", "", err
	}

	return s.app.NatsInfo.RoomStreamName, durableName, nil
}

func (s *NatsService) CreateTranscoderStreamWithConsumer(ctx context.Context, log *logrus.Entry) error {
	// create recorder transcoder worker
	transcoderStream, err := s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        s.app.NatsInfo.Recorder.TranscodingJobs,
		Description: "plugNmeet recorder transcoding jobs",
		Replicas:    s.app.NatsInfo.NumReplicas,
		Retention:   jetstream.WorkQueuePolicy,
		Subjects: []string{
			s.app.NatsInfo.Recorder.TranscodingJobs,
		},
	})
	if err != nil {
		log.WithError(err).Error("error creating recorder transcoder stream")
		return err
	}
	log.Info("Created/Updated recorder transcoder stream")

	_, err = transcoderStream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:    utils.TranscoderConsumerDurable,
		AckPolicy:  jetstream.AckExplicitPolicy,
		AckWait:    maxTranscodingAckWait,
		MaxDeliver: maxTranscodingRetries,
	})
	if err != nil {
		log.WithError(err).Error("error creating recorder transcoder consumer")
		return err
	}
	log.Info("Created/Updated recorder transcoder consumer")

	return nil
}

func (s *NatsService) CreateSummarizeJobStreamWithConsumer(ctx context.Context, log *logrus.Entry) (jetstream.Consumer, error) {
	stream, err := s.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        insights.InsightsJobsStream,
		Description: "plugNmeet meeting summarization jobs",
		Retention:   jetstream.WorkQueuePolicy,
		Replicas:    s.app.NatsInfo.NumReplicas,
		Subjects: []string{
			insights.SummarizeJobQueueSubject,
		},
	})
	if err != nil {
		log.WithError(err).Error("error creating summarize job stream")
		return nil, err
	}
	log.Info("Created/Updated summarize job stream")

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:    insights.SummarizeJobQueueSubject + "-durable",
		AckPolicy:  jetstream.AckExplicitPolicy,
		MaxDeliver: insights.SummarizeJobMaxDeliverNum,
		AckWait:    5 * time.Minute, // just bit higher than default 30 seconds
	})
	if err != nil {
		log.WithError(err).Error("error creating summarize job consumer")
		return nil, err
	}
	log.Info("Created/Updated summarize job consumer")

	return consumer, nil
}

func (s *NatsService) DeleteConsumer(roomId, userId string) {
	durableName := fmt.Sprintf(DurableNameTpl, roomId, userId)
	_ = s.js.DeleteConsumer(s.ctx, s.app.NatsInfo.RoomStreamName, durableName)
}
