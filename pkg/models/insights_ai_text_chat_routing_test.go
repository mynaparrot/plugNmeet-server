package models

import (
	"context"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

// metadataJetStream serves one room metadata document from the room bucket, so the
// models run without a NATS server.
type metadataJetStream struct {
	jetstream.JetStream
	raw []byte
}

func (j metadataJetStream) KeyValue(context.Context, string) (jetstream.KeyValue, error) {
	return metadataKV{raw: j.raw}, nil
}

type metadataKV struct {
	jetstream.KeyValue
	raw []byte
}

func (kv metadataKV) Get(context.Context, string) (jetstream.KeyValueEntry, error) {
	return metadataEntry{raw: kv.raw}, nil
}

type metadataEntry struct {
	jetstream.KeyValueEntry
	raw []byte
}

func (e metadataEntry) Value() []byte { return e.raw }

func newInsightsTestModelWithMetadata(t *testing.T, meta *plugnmeet.RoomMetadata) *InsightsModel {
	t.Helper()

	raw, err := protojson.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal room metadata: %v", err)
	}
	ttl := 2 * time.Hour
	m := newInsightsTestModel(newAIAgentTestRedis(t))
	m.natsService = natsservice.New(natsservice.Args{
		Ctx:    context.Background(),
		App:    &config.AppConfig{NatsInfo: config.NatsInfo{DefaultTTL: &ttl}},
		Js:     metadataJetStream{raw: raw},
		Logger: logrus.New(),
	})

	return m
}

func aiChatRequestFrom(src plugnmeet.InsightsAIRequestSource) *plugnmeet.InsightsAIRequestSource {
	return &src
}

// aiChatRoomMetadata builds the metadata of a room that carries the AI agent
// features, either as a 1-on-1 session room or as a normal room, plus an enabled
// AI text chat feature whose allow-list excludes the test user.
func aiChatRoomMetadata(isBreakoutRoom, isAgentAllowed bool) *plugnmeet.RoomMetadata {
	return &plugnmeet.RoomMetadata{
		IsBreakoutRoom: isBreakoutRoom,
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{
				IsAllow: true,
				AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
					IsAllow:          isAgentAllowed,
					AllowChatMessage: true,
				},
				AiFeatures: &plugnmeet.InsightsAIFeatures{
					IsAllow: true,
					AiTextChatFeatures: &plugnmeet.InsightsAITextChatFeatures{
						IsAllow:           true,
						IsEnabled:         true,
						IsAllowedEveryone: false,
					},
				},
			},
		},
	}
}

// TestExecuteAITextChatRouting locks in that only a 1-on-1 AI agent session room
// forwards an AI Chat message to the agent. A group room or the parent lobby of
// per-user sessions inherits ai_agent_features.is_allow but runs no agent, so its
// text chat must keep using the configured provider instead of failing with
// ai-agent.errors.session-not-started.
func TestExecuteAITextChatRouting(t *testing.T) {
	tests := []struct {
		name string
		meta *plugnmeet.RoomMetadata
		req  *plugnmeet.InsightsAITextChatContent
		want string
	}{
		{
			name: "1-on-1 agent session room routes to the agent",
			meta: aiChatRoomMetadata(true, true),
			req: &plugnmeet.InsightsAITextChatContent{
				Text:        "hello",
				RequestFrom: aiChatRequestFrom(plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT),
			},
			want: "ai-agent.errors.session-not-started",
		},
		{
			name: "normal room with agent features falls through to the provider",
			meta: aiChatRoomMetadata(false, true),
			req: &plugnmeet.InsightsAITextChatContent{
				Text:        "hello",
				RequestFrom: aiChatRequestFrom(plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT),
			},
			want: "insights.ai-text-chat.not-allowed",
		},
		{
			name: "breakout room without the agent falls through to the provider",
			meta: aiChatRoomMetadata(true, false),
			req: &plugnmeet.InsightsAITextChatContent{
				Text:        "hello",
				RequestFrom: aiChatRequestFrom(plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT),
			},
			want: "insights.ai-text-chat.not-allowed",
		},
		{
			name: "notepad request in a 1-on-1 room stays on its own surface",
			meta: aiChatRoomMetadata(true, true),
			req: &plugnmeet.InsightsAITextChatContent{
				Text:        "hello",
				RequestFrom: aiChatRequestFrom(plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_NOTEPAD),
			},
			want: "insights.ai-text-chat.not-allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newInsightsTestModelWithMetadata(t, tt.meta)

			err := m.ExecuteAITextChat(tt.req, "room-1", "user-1")
			if err == nil {
				t.Fatalf("ExecuteAITextChat() returned no error, want %q", tt.want)
			}
			if err.Error() != tt.want {
				t.Fatalf("ExecuteAITextChat() error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
