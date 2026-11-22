package models

import (
	"context"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestBuildSessionDataSaveAckHeader(t *testing.T) {
	key := "page-1"
	targetRoom := "breakout-1"
	requestId := "req-42"

	tests := []struct {
		name       string
		header     *plugnmeet.SessionDataHeader
		success    bool
		wantTarget string
	}{
		{
			name: "request room save",
			header: &plugnmeet.SessionDataHeader{
				DataType:  plugnmeet.SessionDataType_SESSION_DATA_TYPE_WHITEBOARD,
				Key:       &key,
				RequestId: &requestId,
			},
			success: true,
		},
		{
			name: "breakout seeding echoes target room",
			header: &plugnmeet.SessionDataHeader{
				DataType:     plugnmeet.SessionDataType_SESSION_DATA_TYPE_NOTEPAD,
				Key:          &key,
				TargetRoomId: &targetRoom,
				RequestId:    &requestId,
			},
			success:    false,
			wantTarget: targetRoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ack := buildSessionDataSaveAckHeader(tt.header, tt.success)

			if ack.GetDataType() != tt.header.GetDataType() {
				t.Errorf("data_type = %v, want %v", ack.GetDataType(), tt.header.GetDataType())
			}
			if ack.GetKey() != key {
				t.Errorf("key = %q, want %q", ack.GetKey(), key)
			}
			if ack.GetRequestId() != requestId {
				t.Errorf("request_id = %q, want %q", ack.GetRequestId(), requestId)
			}
			if ack.GetTargetRoomId() != tt.wantTarget {
				t.Errorf("target_room_id = %q, want %q", ack.GetTargetRoomId(), tt.wantTarget)
			}
			// success must always be present, otherwise the client cannot tell
			// a failure ack from a missing field.
			if ack.Success == nil || ack.GetSuccess() != tt.success {
				t.Errorf("success = %v, want %v", ack.Success, tt.success)
			}

			// the ack travels as protojson, same convention as fetch responses
			msg, err := protojson.Marshal(ack)
			if err != nil {
				t.Fatalf("marshal ack: %v", err)
			}
			got := new(plugnmeet.SessionDataHeader)
			if err := protojson.Unmarshal(msg, got); err != nil {
				t.Fatalf("unmarshal ack %s: %v", msg, err)
			}
			if got.GetRequestId() != requestId || got.Success == nil || got.GetSuccess() != tt.success {
				t.Errorf("round trip lost ack fields: %s", msg)
			}
		})
	}
}

// After the agent stops, its delegate key is gone but the workspace stays unlocked
// for the 1-on-1 participant, so their whiteboard and notepad saves must stay
// authorized.
func TestIsAIAgentDelegateAuthorizesTheOneOnOneParticipant(t *testing.T) {
	const roomId, participantId, delegateId = "room-1", "user-1", "user-2"

	meta := &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
		InsightsFeatures: &plugnmeet.InsightsFeatures{
			AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
				IsAllow:     true,
				SessionMode: plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS,
				AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
					AllowedParticipantIds: []string{participantId},
				},
			},
		},
	}}
	agentModel, _ := newWorkspaceUnlockTestModel(t, meta)
	m := &NatsModel{
		ctx:          context.Background(),
		aiAgentModel: agentModel,
		logger:       logrus.NewEntry(logrus.New()),
	}

	delegateKey := aiAgentKey(roomId, aiAgentKeyDelegate)
	if err := agentModel.rds.Set(m.ctx, delegateKey, delegateId, 0).Err(); err != nil {
		t.Fatalf("seed delegate: %v", err)
	}
	if !m.isAIAgentDelegate(roomId, delegateId) {
		t.Error("the running delegate must be allowed to save")
	}

	if err := agentModel.rds.Del(m.ctx, delegateKey).Err(); err != nil {
		t.Fatalf("clear delegate: %v", err)
	}
	if !m.isAIAgentDelegate(roomId, participantId) {
		t.Error("the pre-authorized session participant must be allowed to save after the agent stopped")
	}
	if m.isAIAgentDelegate(roomId, "stranger") {
		t.Error("an unrelated user must not be allowed to save")
	}

	withoutAgent := &NatsModel{ctx: context.Background()}
	if withoutAgent.isAIAgentDelegate(roomId, participantId) {
		t.Error("no ai agent model means no delegate")
	}
}
