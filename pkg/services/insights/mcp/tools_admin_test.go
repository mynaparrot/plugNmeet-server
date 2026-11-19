package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

type fakeBreakout struct {
	mu sync.Mutex

	createCalls int
	roomID      string
	actorUserID string
	duration    uint64
	welcomeMsg  string
	titles      []string
	created     int
	createErr   error

	action         string
	breakoutRoomID string
	message        string
	extendMinutes  uint64
	listResult     any
	manageErr      error
}

func (f *fakeBreakout) CreateBreakoutRooms(_ context.Context, roomID, requestedUserID string, durationMinutes uint64, welcomeMsg string, titles []string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.roomID = roomID
	f.actorUserID = requestedUserID
	f.duration = durationMinutes
	f.welcomeMsg = welcomeMsg
	f.titles = titles
	if f.createErr != nil {
		return 0, f.createErr
	}
	if f.created == 0 {
		f.created = len(titles)
	}
	return f.created, nil
}

func (f *fakeBreakout) ManageBreakoutRooms(_ context.Context, roomID, action, breakoutRoomID, message string, durationMinutes uint64) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roomID = roomID
	f.action = action
	f.breakoutRoomID = breakoutRoomID
	f.message = message
	f.extendMinutes = durationMinutes
	if f.manageErr != nil {
		return nil, f.manageErr
	}
	if action == "list" {
		return f.listResult, nil
	}
	return nil, nil
}

type fakeInsights struct {
	mu sync.Mutex

	service     string
	action      string
	spokenLangs []string
	transLangs  []string
	allowedIDs  []string
	synthesis   bool
	defaultLang string
	prompt      string
	err         error
}

func (f *fakeInsights) ConfigureSpeechToText(_ context.Context, _, action string, spokenLangs, transLangs, allowedUserIDs []string, enableSynthesis bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.service, f.action = "speech_to_text", action
	f.spokenLangs, f.transLangs, f.allowedIDs, f.synthesis = spokenLangs, transLangs, allowedUserIDs, enableSynthesis
	return f.err
}

func (f *fakeInsights) ConfigureChatTranslation(_ context.Context, _, action string, transLangs []string, defaultLang string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.service, f.action = "chat_translation", action
	f.transLangs, f.defaultLang = transLangs, defaultLang
	return f.err
}

func (f *fakeInsights) ConfigureMeetingSummarization(_ context.Context, _, action, customPrompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.service, f.action, f.prompt = "meeting_summarization", action, customPrompt
	return f.err
}

type fakeRoomOps struct {
	mu sync.Mutex

	action          string
	userID          string
	durationSeconds uint64
	mediaType       string
	url             string
	err             error
}

func (f *fakeRoomOps) ManageCountdownTimer(_ context.Context, _, userID, action string, durationSeconds uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.action, f.userID, f.durationSeconds = action, userID, durationSeconds
	return f.err
}

func (f *fakeRoomOps) ManageExternalMedia(_ context.Context, _, userID, action, mediaType, rawURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.action, f.userID, f.mediaType, f.url = action, userID, mediaType, rawURL
	return f.err
}

func (f *fakeRoomOps) ManageRecording(_ context.Context, _, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.action = action
	return f.err
}

type fakeParticipants struct {
	mu sync.Mutex

	action          string
	requestedUserID string
	targetUserID    string
	lockService     string
	locked          bool
	err             error
}

func (f *fakeParticipants) ManageParticipants(_ context.Context, _, requestedUserID, action, targetUserID, lockService string, locked bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requestedUserID, f.action, f.targetUserID = requestedUserID, action, targetUserID
	f.lockService, f.locked = lockService, locked
	return f.err
}

// adminDeps returns testDeps plus the four room-administration backends.
func adminDeps() (ToolDependencies, *fakeBreakout, *fakeInsights, *fakeRoomOps, *fakeParticipants) {
	breakout := &fakeBreakout{}
	insightsBackend := &fakeInsights{}
	roomOps := &fakeRoomOps{}
	participants := &fakeParticipants{}

	deps := testDeps()
	deps.Breakout = breakout
	deps.Insights = insightsBackend
	deps.RoomOps = roomOps
	deps.Participants = participants

	return deps, breakout, insightsBackend, roomOps, participants
}

func runTool(t *testing.T, reg *Registry, name, argsJSON string) ToolResult {
	t.Helper()
	exec := NewExecutor(ExecutorOptions{Registry: reg})
	return exec.Execute(context.Background(), ToolCall{
		CallID:      "call-1",
		RoomID:      "room-1",
		SessionID:   "s1",
		AgentUserID: "agent-1",
		ToolName:    name,
		ArgsJSON:    argsJSON,
	})
}

func requireResultKeys(t *testing.T, res ToolResult, want map[string]any) {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.OutputJSON)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.OutputJSON), &out); err != nil {
		t.Fatalf("invalid result JSON %q: %v", res.OutputJSON, err)
	}
	for key, expected := range want {
		got, ok := out[key]
		if !ok {
			t.Fatalf("result %s is missing %q", res.OutputJSON, key)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("result[%q] = %#v, want %#v", key, got, expected)
		}
	}
}

func TestCreateBreakoutRoomsTool(t *testing.T) {
	deps, breakout, _, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolCreateBreakoutRooms,
		`{"duration_minutes":20,"welcome_message":"Work in pairs","room_titles":["Group A","Group B","Group C"]}`)
	requireResultKeys(t, res, map[string]any{
		"ok":               true,
		"rooms_created":    float64(3),
		"duration_minutes": float64(20),
	})

	if breakout.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", breakout.createCalls)
	}
	if breakout.actorUserID != "agent-1" {
		t.Errorf("requested user = %q, want the calling agent", breakout.actorUserID)
	}
	if breakout.roomID != "room-1" || breakout.duration != 20 || breakout.welcomeMsg != "Work in pairs" {
		t.Errorf("unexpected request: room=%q duration=%d welcome=%q", breakout.roomID, breakout.duration, breakout.welcomeMsg)
	}
	if len(breakout.titles) != 3 || breakout.titles[0] != "Group A" {
		t.Errorf("titles = %v", breakout.titles)
	}
}

func TestCreateBreakoutRoomsToolFillsDefaultTitles(t *testing.T) {
	deps, breakout, _, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolCreateBreakoutRooms, `{"duration_minutes":10,"room_count":2}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "rooms_created": float64(2)})

	want := []string{"Room 1", "Room 2"}
	if !reflect.DeepEqual(breakout.titles, want) {
		t.Fatalf("titles = %v, want %v", breakout.titles, want)
	}
}

func TestCreateBreakoutRoomsToolRejectsBadInput(t *testing.T) {
	deps, breakout, _, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"duration too short", `{"duration_minutes":1,"room_count":2}`, "duration_minutes"},
		{"duration too long", `{"duration_minutes":600,"room_count":2}`, "duration_minutes"},
		{"no titles or count", `{"duration_minutes":30}`, "room_titles or room_count"},
		{"duplicate titles", `{"duration_minutes":30,"room_titles":["A","A"]}`, "unique"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runTool(t, reg, ToolCreateBreakoutRooms, tc.args)
			if !res.IsError || !strings.Contains(res.OutputJSON, tc.wantErr) {
				t.Fatalf("result = %s, want an error mentioning %q", res.OutputJSON, tc.wantErr)
			}
		})
	}
	if breakout.createCalls != 0 {
		t.Fatalf("the backend must not be called for invalid input, got %d calls", breakout.createCalls)
	}
}

func TestManageBreakoutRoomsTool(t *testing.T) {
	deps, breakout, _, _, _ := adminDeps()
	breakout.listResult = []string{"room-1-1", "room-1-2"}
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolManageBreakoutRooms, `{"action":"list"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "list"})
	if breakout.action != "list" {
		t.Fatalf("backend action = %q, want list", breakout.action)
	}

	res = runTool(t, reg, ToolManageBreakoutRooms, `{"action":"extend_duration","breakout_room_id":"room-1-1","duration_minutes":5}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "extend_duration"})
	if breakout.breakoutRoomID != "room-1-1" || breakout.extendMinutes != 5 {
		t.Fatalf("extend request = (%q, %d)", breakout.breakoutRoomID, breakout.extendMinutes)
	}

	res = runTool(t, reg, ToolManageBreakoutRooms, `{"action":"end_all"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "end_all"})
	if breakout.action != "end_all" {
		t.Fatalf("backend action = %q, want end_all", breakout.action)
	}
}

func TestManageBreakoutRoomsToolValidatesArguments(t *testing.T) {
	deps, breakout, _, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"message required", `{"action":"broadcast_message"}`, "message is required"},
		{"id required to extend", `{"action":"extend_duration","duration_minutes":5}`, "breakout_room_id is required"},
		{"duration required to extend", `{"action":"extend_duration","breakout_room_id":"room-1-1"}`, "duration_minutes is required"},
		{"id required to end", `{"action":"end_room"}`, "breakout_room_id is required"},
		{"unknown action", `{"action":"pause"}`, "action must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runTool(t, reg, ToolManageBreakoutRooms, tc.args)
			if !res.IsError || !strings.Contains(res.OutputJSON, tc.wantErr) {
				t.Fatalf("result = %s, want an error mentioning %q", res.OutputJSON, tc.wantErr)
			}
		})
	}
	if breakout.action != "" {
		t.Fatalf("the backend must not be called for invalid input, got %q", breakout.action)
	}
}

func TestConfigureSpeechToTextTool(t *testing.T) {
	deps, _, insightsBackend, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolConfigureSpeechToText,
		`{"action":"start_or_update","spoken_langs":["en-US"," en-US "],"trans_langs":["es"],"allowed_user_ids":["user-1"],"enable_synthesis":true}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "service": "speech_to_text", "enabled": true})

	if insightsBackend.action != "start_or_update" {
		t.Fatalf("action = %q", insightsBackend.action)
	}
	if !reflect.DeepEqual(insightsBackend.spokenLangs, []string{"en-US"}) {
		t.Errorf("spoken langs = %v, want the duplicates and blanks removed", insightsBackend.spokenLangs)
	}
	if !reflect.DeepEqual(insightsBackend.transLangs, []string{"es"}) || !insightsBackend.synthesis {
		t.Errorf("translation request = (%v, %t)", insightsBackend.transLangs, insightsBackend.synthesis)
	}
	if !reflect.DeepEqual(insightsBackend.allowedIDs, []string{"user-1"}) {
		t.Errorf("allowed users = %v, want the allowed_user_ids alias accepted", insightsBackend.allowedIDs)
	}
	requireSpeechToTextUserAction(t, res)

	res = runTool(t, reg, ToolConfigureSpeechToText, `{"action":"stop"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "enabled": false})
	if insightsBackend.action != "stop" {
		t.Fatalf("action = %q, want stop", insightsBackend.action)
	}
	if strings.Contains(res.OutputJSON, "user_action_required") {
		t.Errorf("a stop result must not ask for a user action: %s", res.OutputJSON)
	}
}

func TestConfigureSpeechToTextToolAllowedUsers(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		wantIDs []string
	}{
		{
			name:    "everyone",
			args:    `{"action":"start_or_update","spoken_langs":["en-US"],"allowed_users":["everyone"]}`,
			wantIDs: []string{"everyone"},
		},
		{
			name:    "names and ids",
			args:    `{"action":"start_or_update","spoken_langs":["en-US"],"allowed_users":[" Alice ","user-2","Alice"]}`,
			wantIDs: []string{"Alice", "user-2"},
		},
		{
			name:    "both property names merge",
			args:    `{"action":"start_or_update","spoken_langs":["en-US"],"allowed_users":["Alice"],"allowed_user_ids":["user-2"]}`,
			wantIDs: []string{"Alice", "user-2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, insightsBackend, _, _ := adminDeps()
			reg := BuildRoomToolRegistry(fullFeatures(), deps)

			res := runTool(t, reg, ToolConfigureSpeechToText, tc.args)
			requireResultKeys(t, res, map[string]any{"ok": true, "service": "speech_to_text", "enabled": true})
			requireSpeechToTextUserAction(t, res)
			if !reflect.DeepEqual(insightsBackend.allowedIDs, tc.wantIDs) {
				t.Fatalf("allowed users = %v, want %v", insightsBackend.allowedIDs, tc.wantIDs)
			}
		})
	}
}

// requireSpeechToTextUserAction checks the start result tells the model to send
// the user to the "T" icon in the bottom toolbar to pick a language and start.
func requireSpeechToTextUserAction(t *testing.T, res ToolResult) {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(res.OutputJSON), &out); err != nil {
		t.Fatalf("invalid result JSON %q: %v", res.OutputJSON, err)
	}
	instruction, _ := out["user_action_required"].(string)
	for _, want := range []string{"\"T\" icon", "bottom menu bar", "spoken language", "Start"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("user_action_required = %q, want it to mention %q", instruction, want)
		}
	}
}

func TestConfigureSpeechToTextToolValidatesArguments(t *testing.T) {
	deps, _, insightsBackend, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"spoken langs required", `{"action":"start_or_update"}`, "spoken_langs is required"},
		{"allowed users required", `{"action":"start_or_update","spoken_langs":["en-US"]}`, "allowed_users is required for start_or_update"},
		{"blank allowed users required", `{"action":"start_or_update","spoken_langs":["en-US"],"allowed_users":["  "],"allowed_user_ids":[]}`, "allowed_users is required for start_or_update"},
		{"synthesis needs translation", `{"action":"start_or_update","spoken_langs":["en-US"],"allowed_users":["everyone"],"enable_synthesis":true}`, "enable_synthesis requires"},
		{"unknown action", `{"action":"restart"}`, "action must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runTool(t, reg, ToolConfigureSpeechToText, tc.args)
			if !res.IsError || !strings.Contains(res.OutputJSON, tc.wantErr) {
				t.Fatalf("result = %s, want an error mentioning %q", res.OutputJSON, tc.wantErr)
			}
		})
	}
	if insightsBackend.action != "" {
		t.Fatalf("the backend must not be called for invalid input, got %q", insightsBackend.action)
	}
}

func TestConfigureChatTranslationTool(t *testing.T) {
	deps, _, insightsBackend, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolConfigureChatTranslation, `{"action":"start_or_update","trans_langs":["es","fr"],"default_lang":"es"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "service": "chat_translation", "enabled": true})
	if !reflect.DeepEqual(insightsBackend.transLangs, []string{"es", "fr"}) || insightsBackend.defaultLang != "es" {
		t.Fatalf("translation request = (%v, %q)", insightsBackend.transLangs, insightsBackend.defaultLang)
	}

	bad := runTool(t, reg, ToolConfigureChatTranslation, `{"action":"start_or_update"}`)
	if !bad.IsError || !strings.Contains(bad.OutputJSON, "trans_langs is required") {
		t.Fatalf("result = %s, want trans_langs to be required", bad.OutputJSON)
	}

	res = runTool(t, reg, ToolConfigureChatTranslation, `{"action":"stop"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "enabled": false})
	if insightsBackend.action != "stop" {
		t.Fatalf("action = %q, want stop", insightsBackend.action)
	}
}

func TestConfigureMeetingSummarizationTool(t *testing.T) {
	deps, _, insightsBackend, _, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolConfigureMeetingSummarization, `{"action":"start_or_update","summarization_prompt":"Focus on the grammar mistakes"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "service": "meeting_summarization", "enabled": true})
	if insightsBackend.action != "start_or_update" || insightsBackend.prompt != "Focus on the grammar mistakes" {
		t.Fatalf("request = (%q, %q)", insightsBackend.action, insightsBackend.prompt)
	}

	res = runTool(t, reg, ToolConfigureMeetingSummarization, `{"action":"stop"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "enabled": false})

	bad := runTool(t, reg, ToolConfigureMeetingSummarization, `{"action":"pause"}`)
	if !bad.IsError {
		t.Fatalf("result = %s, want an error", bad.OutputJSON)
	}
}

func TestManageCountdownTimerTool(t *testing.T) {
	deps, _, _, roomOps, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolManageCountdownTimer, `{"action":"start","duration_seconds":300}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "start", "duration_seconds": float64(300)})
	if roomOps.action != "start" || roomOps.durationSeconds != 300 || roomOps.userID != "agent-1" {
		t.Fatalf("timer request = (%q, %d, %q)", roomOps.action, roomOps.durationSeconds, roomOps.userID)
	}

	res = runTool(t, reg, ToolManageCountdownTimer, `{"action":"pause"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "pause"})
	if roomOps.action != "pause" {
		t.Fatalf("timer action = %q, want pause", roomOps.action)
	}

	res = runTool(t, reg, ToolManageCountdownTimer, `{"action":"extend","duration_seconds":120}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "extend", "duration_seconds": float64(120)})
	if roomOps.action != "extend" || roomOps.durationSeconds != 120 {
		t.Fatalf("timer request = (%q, %d), want (extend, 120)", roomOps.action, roomOps.durationSeconds)
	}

	res = runTool(t, reg, ToolManageCountdownTimer, `{"action":"extend"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "extend", "duration_seconds": float64(60)})
	if roomOps.action != "extend" || roomOps.durationSeconds != 60 {
		t.Fatalf("timer request = (%q, %d), want (extend, 60)", roomOps.action, roomOps.durationSeconds)
	}
}

func TestManageCountdownTimerToolValidatesArguments(t *testing.T) {
	deps, _, _, roomOps, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"duration too short", `{"action":"start","duration_seconds":5}`, "duration_seconds"},
		{"duration too long", `{"action":"start","duration_seconds":9000}`, "duration_seconds"},
		{"extend duration too short", `{"action":"extend","duration_seconds":5}`, "duration_seconds"},
		{"extend duration too long", `{"action":"extend","duration_seconds":9000}`, "duration_seconds"},
		{"unknown action", `{"action":"reset"}`, "action must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runTool(t, reg, ToolManageCountdownTimer, tc.args)
			if !res.IsError || !strings.Contains(res.OutputJSON, tc.wantErr) {
				t.Fatalf("result = %s, want an error mentioning %q", res.OutputJSON, tc.wantErr)
			}
		})
	}
	if roomOps.action != "" {
		t.Fatalf("the backend must not be called for invalid input, got %q", roomOps.action)
	}
}

func TestManageExternalMediaTool(t *testing.T) {
	deps, _, _, roomOps, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolManageExternalMedia, `{"action":"start","media_type":"external_link","url":"https://example.com/lesson"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "start", "media_type": "external_link"})
	if roomOps.mediaType != "external_link" || roomOps.url != "https://example.com/lesson" {
		t.Fatalf("media request = (%q, %q)", roomOps.mediaType, roomOps.url)
	}

	res = runTool(t, reg, ToolManageExternalMedia, `{"action":"stop","media_type":"media_player"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "stop"})

	missingURL := runTool(t, reg, ToolManageExternalMedia, `{"action":"start","media_type":"media_player"}`)
	if !missingURL.IsError || !strings.Contains(missingURL.OutputJSON, "url is required") {
		t.Fatalf("result = %s, want url to be required", missingURL.OutputJSON)
	}

	badType := runTool(t, reg, ToolManageExternalMedia, `{"action":"start","media_type":"tv","url":"https://example.com/a.mp4"}`)
	if !badType.IsError || !strings.Contains(badType.OutputJSON, "media_type must be one of") {
		t.Fatalf("result = %s, want the media type to be rejected", badType.OutputJSON)
	}
}

func TestManageRecordingTool(t *testing.T) {
	deps, _, _, roomOps, _ := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	res := runTool(t, reg, ToolManageRecording, `{"action":"start"}`)
	requireResultKeys(t, res, map[string]any{"ok": true, "action": "start", "recording": true})
	if roomOps.action != "start" {
		t.Fatalf("recording action = %q, want start", roomOps.action)
	}

	bad := runTool(t, reg, ToolManageRecording, `{"action":"pause"}`)
	if !bad.IsError || !strings.Contains(bad.OutputJSON, "action must be one of") {
		t.Fatalf("result = %s, want the action to be rejected", bad.OutputJSON)
	}
}

func TestManageParticipantsTool(t *testing.T) {
	deps, _, _, _, participants := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	t.Run("mute defaults to the whole room", func(t *testing.T) {
		res := runTool(t, reg, ToolManageParticipants, `{"action":"mute_microphone"}`)
		requireResultKeys(t, res, map[string]any{"ok": true, "target_user_id": "all"})
		if participants.action != "mute_microphone" || participants.targetUserID != "all" {
			t.Fatalf("request = (%q, %q)", participants.action, participants.targetUserID)
		}
		if participants.requestedUserID != "agent-1" {
			t.Fatalf("requested user = %q, want the calling agent", participants.requestedUserID)
		}
	})

	t.Run("locked defaults to true", func(t *testing.T) {
		res := runTool(t, reg, ToolManageParticipants, `{"action":"update_lock_settings","lock_service":"mic"}`)
		requireResultKeys(t, res, map[string]any{"ok": true, "lock_service": "mic", "locked": true})
		if !participants.locked || participants.targetUserID != "all" {
			t.Fatalf("lock request = (%q, %t)", participants.targetUserID, participants.locked)
		}
	})

	t.Run("explicit unlock", func(t *testing.T) {
		res := runTool(t, reg, ToolManageParticipants, `{"action":"update_lock_settings","lock_service":"whiteboard","locked":false,"target_user_id":"user-1"}`)
		requireResultKeys(t, res, map[string]any{"ok": true, "locked": false})
		if participants.locked || participants.targetUserID != "user-1" {
			t.Fatalf("lock request = (%q, %t)", participants.targetUserID, participants.locked)
		}
	})

	t.Run("approve one waiting user", func(t *testing.T) {
		res := runTool(t, reg, ToolManageParticipants, `{"action":"approve_waiting_users","target_user_id":"user-2"}`)
		requireResultKeys(t, res, map[string]any{"ok": true})
		if participants.action != "approve_waiting_users" || participants.targetUserID != "user-2" {
			t.Fatalf("request = (%q, %q)", participants.action, participants.targetUserID)
		}
	})
}

func TestManageParticipantsToolValidatesArguments(t *testing.T) {
	deps, _, _, _, participants := adminDeps()
	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	cases := []struct {
		name    string
		args    string
		wantErr string
	}{
		{"presenter needs one target", `{"action":"switch_presenter","target_user_id":"all"}`, "one participant is required"},
		{"approve needs one target", `{"action":"approve_waiting_users"}`, "target_user_id of one participant is required"},
		{"lock needs a service", `{"action":"update_lock_settings","locked":true}`, "lock_service must be one of"},
		{"unknown action", `{"action":"remove"}`, "action must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runTool(t, reg, ToolManageParticipants, tc.args)
			if !res.IsError || !strings.Contains(res.OutputJSON, tc.wantErr) {
				t.Fatalf("result = %s, want an error mentioning %q", res.OutputJSON, tc.wantErr)
			}
		})
	}
	if participants.action != "" {
		t.Fatalf("the backend must not be called for invalid input, got %q", participants.action)
	}
}

func TestAdminToolsAreOnlyRegisteredWithTheirBackend(t *testing.T) {
	adminTools := []string{
		ToolCreateBreakoutRooms, ToolManageBreakoutRooms,
		ToolConfigureSpeechToText, ToolConfigureChatTranslation, ToolConfigureMeetingSummarization,
		ToolManageCountdownTimer, ToolManageExternalMedia, ToolManageRecording,
		ToolManageParticipants,
	}

	plain := BuildRoomToolRegistry(fullFeatures(), testDeps())
	for _, name := range adminTools {
		if hasTool(plain, name) {
			t.Errorf("%s must not be registered without its backend", name)
		}
	}

	deps, _, _, _, _ := adminDeps()
	full := BuildRoomToolRegistry(fullFeatures(), deps)
	for _, name := range adminTools {
		if !hasTool(full, name) {
			t.Errorf("%s must be registered when its backend is wired", name)
		}
	}
}

// A 1-on-1 voice agent keeps only the countdown timer from the room-admin tool
// set; everything else belongs to the administrator's AI text chat.
func TestAIAgentScopeSkipsAdminTools(t *testing.T) {
	deps, _, _, _, _ := adminDeps()
	deps.Breakout = nil
	deps.Insights = nil
	deps.Participants = nil
	deps.MCPScope = MCPScopeAIAgent

	reg := BuildRoomToolRegistry(fullFeatures(), deps)

	if !hasTool(reg, ToolManageCountdownTimer) {
		t.Error("manage_countdown_timer must be registered for the voice agent")
	}
	for _, name := range []string{
		ToolCreateBreakoutRooms, ToolManageBreakoutRooms,
		ToolConfigureSpeechToText, ToolConfigureChatTranslation, ToolConfigureMeetingSummarization,
		ToolManageExternalMedia, ToolManageRecording,
		ToolManageParticipants,
	} {
		if hasTool(reg, name) {
			t.Errorf("%s must not be registered for the voice agent", name)
		}
	}
}

func TestAdminBackendErrorsBecomeToolErrors(t *testing.T) {
	deps, breakout, insightsBackend, roomOps, participants := adminDeps()
	breakout.createErr = errors.New("no room")
	insightsBackend.err = errors.New("service disabled")
	roomOps.err = errors.New("timer off")
	participants.err = errors.New("not allowed")

	reg := BuildRoomToolRegistry(fullFeatures(), deps)
	calls := []struct {
		name string
		args string
	}{
		{ToolCreateBreakoutRooms, `{"duration_minutes":10,"room_count":1}`},
		{ToolConfigureChatTranslation, `{"action":"start_or_update","trans_langs":["es"]}`},
		{ToolManageRecording, `{"action":"stop"}`},
		{ToolManageParticipants, `{"action":"mute_microphone"}`},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			res := runTool(t, reg, tc.name, tc.args)
			if !res.IsError || !strings.Contains(res.OutputJSON, `"ok":false`) {
				t.Fatalf("result = %s, want a tool error", res.OutputJSON)
			}
		})
	}
}

func TestBuildAdminTextChatToolRegistry(t *testing.T) {
	deps, _, _, _, _ := adminDeps()
	features := fullFeatures()
	features.McpServers = nil

	reg := BuildAdminTextChatToolRegistry(features, deps)

	want := []string{
		ToolCreatePoll, ToolDraftQuiz, ToolPublishQuiz, ToolClosePoll, ToolGetPollResults,
		ToolSendChatMessage, ToolGetRoomInfo, ToolRecordLearningProgress,
		ToolNotepadWrite, ToolNotepadRead,
		ToolWhiteboardWriteContent, ToolWhiteboardDrawShapes, ToolWhiteboardDrawFreehandSVG,
		ToolWhiteboardDrawDiagram, ToolWhiteboardLaserPoint, ToolWhiteboardManageBoard,
		ToolWhiteboardCaptureSnapshot, ToolSetWorkspaceLock, ToolFetchExternalKnowledge,
		ToolCreateBreakoutRooms, ToolManageBreakoutRooms,
		ToolConfigureSpeechToText, ToolConfigureChatTranslation, ToolConfigureMeetingSummarization,
		ToolManageCountdownTimer, ToolManageExternalMedia, ToolManageRecording,
		ToolManageParticipants,
	}
	for _, name := range want {
		if !hasTool(reg, name) {
			t.Errorf("%s must be registered in the admin text chat registry", name)
		}
	}
	if hasTool(reg, ToolCreateQuiz) {
		t.Error("create_quiz must be replaced by the approval flow in a text chat session")
	}
}

func TestBuildAdminTextChatToolRegistryKeepsKnowledgeGating(t *testing.T) {
	deps, _, _, _, _ := adminDeps()

	features := &plugnmeet.InsightsAIAgentFeatures{AllowWebFetch: true}
	reg := BuildAdminTextChatToolRegistry(features, deps)
	if hasTool(reg, ToolFetchExternalKnowledge) {
		t.Error("fetch_external_knowledge needs at least one allowed url")
	}

	features.AllowedKnowledgeUrls = []string{"https://docs.example.com/"}
	reg = BuildAdminTextChatToolRegistry(features, deps)
	if !hasTool(reg, ToolFetchExternalKnowledge) {
		t.Error("fetch_external_knowledge must follow the allowed urls")
	}
}
