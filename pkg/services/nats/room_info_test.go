package natsservice

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPrepareRoomMetadataForWaitingUser(t *testing.T) {
	s := &NatsService{}

	raw := `{
		"room_title": "test room",
		"welcome_message": "welcome",
		"logout_url": "https://example.com/logout",
		"is_recording": true,
		"webhook_url": "https://example.com/webhook",
		"metadata_id": "abc",
		"extra_data": {"foo": "bar"},
		"room_features": {
			"allow_webcams": true,
			"mute_on_start": true,
			"allow_screen_share": true,
			"admin_only_webcams": true,
			"allow_virtual_bg": false,
			"waiting_room_features": {"is_active": true, "waiting_room_msg": "please wait"},
			"end_to_end_encryption_features": {
				"is_enabled": true,
				"included_chat_messages": true,
				"included_whiteboard": true,
				"encryption_key": "secret",
				"enabled_self_insert_encryption_key": true
			}
		}
	}`

	out, err := s.PrepareRoomMetadataForWaitingUser(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	meta := new(plugnmeet.RoomMetadata)
	if err := protojson.Unmarshal([]byte(out), meta); err != nil {
		t.Fatalf("failed to unmarshal redacted metadata: %v", err)
	}

	if meta.GetRoomTitle() != "test room" || meta.GetWelcomeMessage() != "welcome" || meta.GetLogoutUrl() != "https://example.com/logout" {
		t.Errorf("basic fields not preserved: %s", out)
	}
	if meta.GetMetadataId() != "" {
		t.Errorf("unexpected metadataId: %s", meta.GetMetadataId())
	}
	if meta.GetWebhookUrl() != "" || meta.GetIsRecording() || len(meta.GetExtraData()) != 0 {
		t.Errorf("sensitive room metadata leaked: %s", out)
	}

	rf := meta.GetRoomFeatures()
	if rf == nil {
		t.Fatal("room features missing")
	}
	if !rf.GetAllowWebcams() || !rf.GetMuteOnStart() || !rf.GetAdminOnlyWebcams() {
		t.Errorf("expected room features not preserved: %s", out)
	}
	if rf.GetAllowVirtualBg() {
		t.Errorf("allow_virtual_bg not preserved: %s", out)
	}
	if rf.GetWaitingRoomFeatures() == nil || !rf.GetWaitingRoomFeatures().GetIsActive() || rf.GetWaitingRoomFeatures().GetWaitingRoomMsg() != "please wait" {
		t.Errorf("waiting room features not preserved: %s", out)
	}
	if rf.GetRecordingFeatures() != nil || rf.GetAllowScreenShare() {
		t.Errorf("unnecessary room features leaked: %s", out)
	}

	e2ee := rf.GetEndToEndEncryptionFeatures()
	if e2ee == nil || !e2ee.GetIsEnabled() || !e2ee.GetEnabledSelfInsertEncryptionKey() {
		t.Fatalf("e2ee flags not preserved: %s", out)
	}
	if e2ee.GetEncryptionKey() != "" || e2ee.GetIncludedChatMessages() || e2ee.GetIncludedWhiteboard() {
		t.Errorf("e2ee sensitive data leaked: %s", out)
	}
}

func TestPrepareRoomMetadataForWaitingUser_InvalidInput(t *testing.T) {
	s := &NatsService{}
	if _, err := s.PrepareRoomMetadataForWaitingUser("{invalid"); err == nil {
		t.Error("expected error for invalid metadata")
	}
	if _, err := s.RedactRoomMetadataForWaitingUser(nil); err == nil {
		t.Error("expected error for nil metadata")
	}
}
