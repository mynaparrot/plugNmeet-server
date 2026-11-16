package models

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
)

type submittedClientAPI struct {
	roomID       string
	targetUserID string
	route        string
	payload      proto.Message
	argsJSON     string
}

// newClientAPITestModel wires the client API submitter to a recorder, so the
// backends can be tested without a browser round-trip.
func newClientAPITestModel(t *testing.T) (*AIAgentModel, *[]submittedClientAPI) {
	t.Helper()

	calls := make([]submittedClientAPI, 0, 2)
	model := newAIAgentTestModel(newAIAgentTestRedis(t))
	model.clientAPISubmitter = func(_ context.Context, roomID, targetUserID, route string, payload proto.Message) error {
		argsJSON, err := buildClientAPISubmitArgs(route, payload)
		if err != nil {
			return err
		}
		calls = append(calls, submittedClientAPI{
			roomID:       roomID,
			targetUserID: targetUserID,
			route:        route,
			payload:      payload,
			argsJSON:     argsJSON,
		})
		return nil
	}

	return model, &calls
}

func lastClientAPICall(t *testing.T, calls *[]submittedClientAPI) submittedClientAPI {
	t.Helper()

	if len(*calls) == 0 {
		t.Fatal("no client_api_submit call was dispatched")
	}
	return (*calls)[len(*calls)-1]
}

// assertClientAPIArgsJSON compares two JSON documents semantically.
func assertClientAPIArgsJSON(t *testing.T, got, want string) {
	t.Helper()

	var gotVal, wantVal any
	if err := json.Unmarshal([]byte(got), &gotVal); err != nil {
		t.Fatalf("the dispatched arguments are not valid JSON: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
		t.Fatalf("the expected arguments are not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("dispatched arguments = %s, want %s", got, want)
	}
}

func TestBuildClientAPISubmitArgs(t *testing.T) {
	if clientAPISubmitToolName != "client_api_submit" {
		t.Errorf("tool name = %q, want client_api_submit", clientAPISubmitToolName)
	}

	args, err := buildClientAPISubmitArgs(clientAPIRouteTranscriptionConfigure, &plugnmeet.InsightsTranscriptionConfigReq{
		IsEnabled:            true,
		IsEnabledTranslation: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertClientAPIArgsJSON(t, args,
		`{"route":"insights/transcription/configure","payload":{"isEnabled":true,"isEnabledTranslation":true}}`)

	args, err = buildClientAPISubmitArgs(clientAPIRouteRecording, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertClientAPIArgsJSON(t, args, `{"route":"recording"}`)
}

func TestCreateBreakoutRoomsSubmitsClientAPI(t *testing.T) {
	model, calls := newClientAPITestModel(t)
	backend := aiAgentBreakoutBackend{m: model, actorUserID: "admin-1"}

	created, err := backend.CreateBreakoutRooms(context.Background(), "room-1", "", 15, "  Welcome  ", []string{"Group A", "Group B"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created != 2 {
		t.Errorf("created = %d, want 2", created)
	}
	if len(*calls) != 1 {
		t.Fatalf("dispatched %d client API calls, want 1", len(*calls))
	}

	call := lastClientAPICall(t, calls)
	if call.roomID != "room-1" || call.targetUserID != "admin-1" || call.route != clientAPIRouteBreakoutCreate {
		t.Errorf("dispatched call = %+v, want room-1/admin-1/%s", call, clientAPIRouteBreakoutCreate)
	}

	req, ok := call.payload.(*plugnmeet.CreateBreakoutRoomsReq)
	if !ok {
		t.Fatalf("payload = %T, want *plugnmeet.CreateBreakoutRoomsReq", call.payload)
	}
	if req.GetRoomId() != "room-1" || req.GetRequestedUserId() != "admin-1" || req.GetDuration() != 15 {
		t.Errorf("request = %+v, want room-1/admin-1/15min", req)
	}
	if !req.GetAllowReturnToMainRoom() || req.GetWelcomeMsg() != "Welcome" {
		t.Errorf("request = %+v, want allow_return_to_main_room and the trimmed welcome message", req)
	}
	if len(req.GetRooms()) != 2 || req.GetRooms()[0].GetId() != "1" || req.GetRooms()[1].GetTitle() != "Group B" {
		t.Errorf("rooms = %+v, want one uniquely identified room per title", req.GetRooms())
	}
	assertClientAPIArgsJSON(t, call.argsJSON,
		`{"route":"breakoutRoom/create","payload":{"roomId":"room-1","requestedUserId":"admin-1","duration":"15","rooms":[{"id":"1","title":"Group A","duration":"15"},{"id":"2","title":"Group B","duration":"15"}],"allowReturnToMainRoom":true,"welcomeMsg":"Welcome"}}`)

	if _, err := backend.CreateBreakoutRooms(context.Background(), "room-1", "admin-1", 10, "", nil); err == nil {
		t.Error("an empty title list must be rejected")
	}
	if len(*calls) != 1 {
		t.Error("a rejected request must not be dispatched")
	}
}

func TestConfigureSpeechToTextSubmitsClientAPI(t *testing.T) {
	model, calls := newClientAPITestModel(t)
	backend := aiAgentInsightsBackend{m: model, actorUserID: "admin-1"}
	ctx := context.Background()

	err := backend.ConfigureSpeechToText(ctx, "room-1", "start_or_update", []string{"en-US"}, []string{"es"}, []string{"user-1"}, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call := lastClientAPICall(t, calls)
	if call.targetUserID != "admin-1" || call.route != clientAPIRouteTranscriptionConfigure {
		t.Errorf("dispatched call = %+v, want admin-1/%s", call, clientAPIRouteTranscriptionConfigure)
	}
	req, ok := call.payload.(*plugnmeet.InsightsTranscriptionConfigReq)
	if !ok {
		t.Fatalf("payload = %T, want *plugnmeet.InsightsTranscriptionConfigReq", call.payload)
	}
	if !req.GetIsEnabled() || !req.GetIsEnabledTranslation() || !req.GetIsEnabledSpeechSynthesis() {
		t.Errorf("request = %+v, want transcription, translation and synthesis enabled", req)
	}
	if req.GetDefaultSubtitleLang() != "en-US" {
		t.Errorf("default subtitle lang = %q, want en-US", req.GetDefaultSubtitleLang())
	}
	if !reflect.DeepEqual(req.GetAllowedSpeechUsers(), []string{"user-1"}) {
		t.Errorf("allowed speech users = %v, want [user-1]", req.GetAllowedSpeechUsers())
	}

	if err := backend.ConfigureSpeechToText(ctx, "room-1", "stop", nil, nil, nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call = lastClientAPICall(t, calls)
	if call.route != clientAPIRouteTranscriptionEnd || call.payload != nil {
		t.Errorf("stop call = %+v, want route %s without payload", call, clientAPIRouteTranscriptionEnd)
	}
	assertClientAPIArgsJSON(t, call.argsJSON, `{"route":"insights/transcription/end"}`)

	dispatched := len(*calls)
	if err := backend.ConfigureSpeechToText(ctx, "room-1", "start_or_update", nil, nil, nil, false); err == nil {
		t.Error("an empty participant selection must be rejected")
	}
	if err := backend.ConfigureSpeechToText(ctx, "room-1", "pause", nil, nil, nil, false); err == nil {
		t.Error("an unknown action must be rejected")
	}
	if len(*calls) != dispatched {
		t.Error("rejected requests must not be dispatched")
	}
}

func TestConfigureChatTranslationSubmitsClientAPI(t *testing.T) {
	model, calls := newClientAPITestModel(t)
	backend := aiAgentInsightsBackend{m: model, actorUserID: "admin-1"}
	ctx := context.Background()

	if err := backend.ConfigureChatTranslation(ctx, "room-1", "start_or_update", []string{"es", "fr"}, "es"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := lastClientAPICall(t, calls)
	if call.targetUserID != "admin-1" || call.route != clientAPIRouteChatTranslationConfigure {
		t.Errorf("dispatched call = %+v, want admin-1/%s", call, clientAPIRouteChatTranslationConfigure)
	}
	req, ok := call.payload.(*plugnmeet.InsightsChatTranslationConfigReq)
	if !ok {
		t.Fatalf("payload = %T, want *plugnmeet.InsightsChatTranslationConfigReq", call.payload)
	}
	if !req.GetIsEnabled() || req.GetDefaultLang() != "es" || !reflect.DeepEqual(req.GetAllowedTransLangs(), []string{"es", "fr"}) {
		t.Errorf("request = %+v, want enabled translation to es/fr with es default", req)
	}

	if err := backend.ConfigureChatTranslation(ctx, "room-1", "stop", nil, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call = lastClientAPICall(t, calls)
	if call.route != clientAPIRouteChatTranslationEnd || call.payload != nil {
		t.Errorf("stop call = %+v, want route %s without payload", call, clientAPIRouteChatTranslationEnd)
	}
}

func TestConfigureMeetingSummarizationSubmitsClientAPI(t *testing.T) {
	model, calls := newClientAPITestModel(t)
	backend := aiAgentInsightsBackend{m: model, actorUserID: "admin-1"}
	ctx := context.Background()

	if err := backend.ConfigureMeetingSummarization(ctx, "room-1", "start_or_update", "Focus on grammar"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := lastClientAPICall(t, calls)
	if call.targetUserID != "admin-1" || call.route != clientAPIRouteMeetingSummarizationConfigure {
		t.Errorf("dispatched call = %+v, want admin-1/%s", call, clientAPIRouteMeetingSummarizationConfigure)
	}
	req, ok := call.payload.(*plugnmeet.InsightsAIMeetingSummarizationConfigReq)
	if !ok {
		t.Fatalf("payload = %T, want *plugnmeet.InsightsAIMeetingSummarizationConfigReq", call.payload)
	}
	if !req.GetIsEnabled() || req.GetSummarizationPrompt() != "Focus on grammar" {
		t.Errorf("request = %+v, want the enabled summarization prompt", req)
	}

	if err := backend.ConfigureMeetingSummarization(ctx, "room-1", "stop", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call = lastClientAPICall(t, calls)
	if call.route != clientAPIRouteMeetingSummarizationEnd || call.payload != nil {
		t.Errorf("stop call = %+v, want route %s without payload", call, clientAPIRouteMeetingSummarizationEnd)
	}
}

func TestManageRecordingSubmitsClientAPI(t *testing.T) {
	model, calls := newClientAPITestModel(t)
	backend := aiAgentRoomOpsBackend{m: model, actorUserID: "admin-1"}
	ctx := context.Background()

	if err := backend.ManageRecording(ctx, "room-1", "start"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	call := lastClientAPICall(t, calls)
	if call.targetUserID != "admin-1" || call.route != clientAPIRouteRecording {
		t.Errorf("dispatched call = %+v, want admin-1/%s", call, clientAPIRouteRecording)
	}
	req, ok := call.payload.(*plugnmeet.RecordingReq)
	if !ok {
		t.Fatalf("payload = %T, want *plugnmeet.RecordingReq", call.payload)
	}
	if req.GetTask() != plugnmeet.RecordingTasks_START_RECORDING || req.GetRoomId() != "room-1" {
		t.Errorf("request = %+v, want a start recording task for room-1", req)
	}
	if req.GetRecordingVariant() != plugnmeet.CloudRecordingVariants_FULL_SCREEN_CLOUD_RECORDING {
		t.Errorf("variant = %v, want full screen cloud recording", req.GetRecordingVariant())
	}
	// START_RECORDING is the enum default, so protojson omits it and the
	// browser's schema falls back to it.
	assertClientAPIArgsJSON(t, call.argsJSON,
		`{"route":"recording","payload":{"roomId":"room-1","recordingVariant":"FULL_SCREEN_CLOUD_RECORDING"}}`)

	if err := backend.ManageRecording(ctx, "room-1", "stop"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	req, ok = lastClientAPICall(t, calls).payload.(*plugnmeet.RecordingReq)
	if !ok || req.GetTask() != plugnmeet.RecordingTasks_STOP_RECORDING {
		t.Errorf("stop request = %+v, want a stop recording task", req)
	}

	dispatched := len(*calls)
	if err := backend.ManageRecording(ctx, "room-1", "pause"); err == nil {
		t.Error("an unknown action must be rejected")
	}
	if len(*calls) != dispatched {
		t.Error("a rejected request must not be dispatched")
	}
}
