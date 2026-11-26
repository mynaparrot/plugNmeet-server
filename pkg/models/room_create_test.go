package models

import (
	"context"
	"errors"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// TestValidateRoomAIAgentFeaturesAutoGenUserId proves that a controlled-users
// room cannot auto-generate participant ids, because a generated id would
// bypass the pre-authorized roster.
func TestValidateRoomAIAgentFeaturesAutoGenUserId(t *testing.T) {
	newReq := func(mode plugnmeet.AIAgentSessionMode, autoGen *bool, cfg *plugnmeet.AIAgentAutoSessionConfig) *plugnmeet.CreateRoomReq {
		return &plugnmeet.CreateRoomReq{
			RoomId: "room-1",
			Metadata: &plugnmeet.RoomMetadata{
				RoomFeatures: &plugnmeet.RoomCreateFeatures{
					AutoGenUserId: autoGen,
					InsightsFeatures: &plugnmeet.InsightsFeatures{
						IsAllow: true,
						AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
							IsAllow:      true,
							SystemPrompt: "teach fractions",
							Topic:        "fractions",
							SessionMode:  mode,
							// a room-managed provider keeps the validator off the server config
							CustomProvider:    &plugnmeet.InsightsAIAgentCustomProvider{Provider: "google", ApiKey: "google-key"},
							AutoSessionConfig: cfg,
						},
					},
				},
			},
		}
	}
	m := &RoomModel{app: &config.AppConfig{}, aiAgentModel: new(AIAgentModel), logger: logrus.NewEntry(logrus.New())}
	ctx := context.Background()

	rejected := newReq(modeControlled, new(true), controlledConfig("learner-1"))
	if err := m.validateAIAgentFeatures(ctx, rejected, m.logger); !errors.Is(err, config.ErrAIAgentAutoGenUserIdUnsupported) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentAutoGenUserIdUnsupported)
	}

	allowed := []struct {
		name string
		req  *plugnmeet.CreateRoomReq
	}{
		{name: "normal room", req: newReq(modeNormalRoom, new(true), nil)},
		{name: "open per user", req: newReq(modeOpenPerUse, new(true), openPerUserConfig(3))},
		{name: "controlled with auto gen disabled", req: newReq(modeControlled, new(false), controlledConfig("learner-1"))},
		{name: "controlled with auto gen unset", req: newReq(modeControlled, nil, controlledConfig("learner-1"))},
	}

	for _, tt := range allowed {
		t.Run(tt.name, func(t *testing.T) {
			if err := m.validateAIAgentFeatures(ctx, tt.req, m.logger); err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}
