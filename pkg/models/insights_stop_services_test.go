package models

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// stopInsightsRoomMetadata describes a room with every insights service running.
func stopInsightsRoomMetadata() *plugnmeet.RoomMetadata {
	return &plugnmeet.RoomMetadata{
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{
				IsAllow: true,
				TranscriptionFeatures: &plugnmeet.InsightsTranscriptionFeatures{
					IsAllow:                  true,
					IsEnabled:                true,
					IsEnabledTranslation:     true,
					IsEnabledSpeechSynthesis: true,
				},
				ChatTranslationFeatures: &plugnmeet.InsightsChatTranslationFeatures{
					IsAllow:   true,
					IsEnabled: true,
				},
				AiFeatures: &plugnmeet.InsightsAIFeatures{
					IsAllow: true,
					AiTextChatFeatures: &plugnmeet.InsightsAITextChatFeatures{
						IsAllow:   true,
						IsEnabled: true,
					},
					MeetingSummarizationFeatures: &plugnmeet.InsightsAIMeetingSummarizationFeatures{
						IsAllow:   true,
						IsEnabled: true,
					},
				},
				AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
					IsAllow: true,
				},
			},
		},
	}
}

// newStopRoomInsightsTestModel builds an insights model whose room metadata and
// live cost live in memory, so a stop can be inspected afterwards.
func newStopRoomInsightsTestModel(t *testing.T, roomId string) (*InsightsModel, *memoryJetStream, redis.UniversalClient, *gorm.DB, *dbmodels.RoomInfo) {
	t.Helper()

	raw, err := protojson.Marshal(stopInsightsRoomMetadata())
	if err != nil {
		t.Fatalf("marshal room metadata: %v", err)
	}
	js := &memoryJetStream{
		entries: map[string][]byte{natsservice.RoomInfoKeyPrefix + natsservice.RoomMetadataKey: raw},
	}

	ctx := memoryJetStreamContext(t)
	rc := newAIAgentTestRedis(t)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("failed to open sqlite test db: %v", err)
	}
	createTestSchemaFromInstallDump(t, gdb, "room_info", "room_artifacts")

	row := &dbmodels.RoomInfo{RoomId: roomId, Sid: roomId + "-sid", IsRunning: 1, RoomTitle: roomId}
	if err := gdb.Create(row).Error; err != nil {
		t.Fatalf("failed to create the room row: %v", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	ttl := 2 * time.Hour
	storagePath := t.TempDir()
	app := &config.AppConfig{
		RedisInfo:         connections.RedisInfo{DefaultTTL: &ttl},
		NatsInfo:          config.NatsInfo{DefaultTTL: &ttl},
		Insights:          &config.InsightsConfig{},
		ArtifactsSettings: &config.ArtifactsSettings{StoragePath: &storagePath},
	}
	ns := natsservice.New(natsservice.Args{
		Ctx:    ctx,
		App:    app,
		Js:     js,
		Logger: logger,
	})
	rs := redisservice.New(redisservice.Args{Ctx: ctx, Rc: rc, App: app, Logger: logger})
	ds := dbservice.New(dbservice.Args{Ctx: ctx, Db: gdb, Logger: logger})

	artifactModel := &ArtifactModel{
		ctx:            ctx,
		app:            app,
		ds:             ds,
		rs:             rs,
		natsService:    ns,
		analyticsModel: &AnalyticsModel{app: app},
		log:            logger.WithField("model", "artifact"),
	}
	aiAgentModel := &AIAgentModel{
		ctx:           ctx,
		appConfig:     app,
		rds:           rc,
		redisService:  rs,
		natsService:   ns,
		artifactModel: artifactModel,
		logger:        logger.WithField("model", "ai_agent"),
		stopTimers:    make(map[string]*time.Timer),
	}

	m := &InsightsModel{
		ctx:           ctx,
		appConfig:     app,
		rds:           rc,
		redisService:  rs,
		natsService:   ns,
		artifactModel: artifactModel,
		aiAgentModel:  aiAgentModel,
		logger:        logger.WithField("model", "insights"),
	}

	return m, js, rc, gdb, row
}

// TestStopRoomInsightsServicesDisablesInsightsAndClearsLiveCost locks in that
// stopping the services of a running room turns every insights flag off, harvests
// the usage (which clears the live cost) and leaves the room itself running.
func TestStopRoomInsightsServicesDisablesInsightsAndClearsLiveCost(t *testing.T) {
	const roomId = "room-1"
	m, js, rc, gdb, row := newStopRoomInsightsTestModel(t, roomId)

	ctx := context.Background()
	if err := m.redisService.SetInsightsLiveCost(ctx, roomId, redisservice.LiveCostFieldTranscription, 1.25); err != nil {
		t.Fatalf("failed to seed the live cost: %v", err)
	}

	m.StopRoomInsightsServices(ctx, roomId, row.Sid, row.ID)

	features := storedRoomMetadata(t, js).GetRoomFeatures().GetInsightsFeatures()
	if tf := features.GetTranscriptionFeatures(); tf.GetIsEnabled() || tf.GetIsEnabledTranslation() || tf.GetIsEnabledSpeechSynthesis() {
		t.Errorf("transcription = (enabled=%v, translation=%v, synthesis=%v), want all disabled",
			tf.GetIsEnabled(), tf.GetIsEnabledTranslation(), tf.GetIsEnabledSpeechSynthesis())
	}
	if features.GetChatTranslationFeatures().GetIsEnabled() {
		t.Error("chat translation is still enabled")
	}
	if features.GetAiFeatures().GetAiTextChatFeatures().GetIsEnabled() {
		t.Error("ai text chat is still enabled")
	}
	if features.GetAiFeatures().GetMeetingSummarizationFeatures().GetIsEnabled() {
		t.Error("meeting summarization is still enabled")
	}
	// The usage harvest runs at the end of the stop, so the live cost is gone.
	if exists, err := rc.Exists(ctx, fmt.Sprintf(redisservice.InsightsLiveCostKey, roomId)).Result(); err != nil {
		t.Fatalf("failed to check the live cost key: %v", err)
	} else if exists != 0 {
		t.Error("the live cost key was not cleaned up")
	}
	if active, err := rc.SIsMember(ctx, redisservice.InsightsActiveRoomsKey, roomId).Result(); err != nil {
		t.Fatalf("failed to check the active insights rooms set: %v", err)
	} else if active {
		t.Error("the room is still registered as an active insights room")
	}

	// The room itself must keep running: only its insights services stopped.
	if _, ok := js.value(natsservice.RoomInfoKeyPrefix + natsservice.RoomMetadataKey); !ok {
		t.Fatal("the room metadata was removed, the room must keep running")
	}
	stored := new(dbmodels.RoomInfo)
	if err := gdb.First(stored, row.ID).Error; err != nil {
		t.Fatalf("failed to reload the room row: %v", err)
	}
	if stored.IsRunning != 1 {
		t.Error("the room was marked as not running, but only its insights services had to stop")
	}
}
