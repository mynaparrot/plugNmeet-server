package config

import "testing"

// TestIntegratorAIAgentCreateRoomMsgs proves a CreateRoom refusal of an
// ai_agent_features block reaches an integrator as curated English instead of
// the generic fallback.
func TestIntegratorAIAgentCreateRoomMsgs(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrAIAgentNotAllowed, MsgAuthAIAgentNotAllowed},
		{ErrAIAgentE2EEUnsupported, MsgAuthAIAgentE2EEUnsupported},
		{ErrAIAgentPromptRequired, MsgAuthAIAgentPromptRequired},
		{ErrAIAgentTopicRequired, MsgAuthAIAgentTopicRequired},
		{ErrAIAgentProviderMissing, MsgAuthAIAgentProviderMissing},
		{ErrAIAgentInvalidMCPServer, MsgAuthAIAgentInvalidMCPServer},
		{ErrAIAgentAllowedUsersRequired, MsgAuthAIAgentAllowedUsersRequired},
		{ErrAIAgentMaxConcurrentSessions, MsgAuthAIAgentMaxConcurrentSessions},
		{ErrAIAgentAutoGenUserIdUnsupported, MsgAuthAIAgentAutoGenUserIdUnsupported},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			got := IntegratorMsg(tt.err.Error())
			if got != tt.want {
				t.Fatalf("IntegratorMsg(%q) = %q, want %q", tt.err.Error(), got, tt.want)
			}
			if got == MsgIntegratorUnexpected {
				t.Fatalf("%q fell back to the generic integrator message", tt.err)
			}
		})
	}
}

// TestIntegratorMsgAndKey proves the msg/msg_key contract of an integrator
// response: every mapped message answers with its curated English text and its
// own i18n key, and everything unknown falls back to the generic pair.
func TestIntegratorMsgAndKey(t *testing.T) {
	t.Run("known messages keep their key", func(t *testing.T) {
		for msg, want := range integratorMsgs {
			text, key := IntegratorMsgAndKey(msg)
			if text != want {
				t.Errorf("IntegratorMsgAndKey(%q) text = %q, want %q", msg, text, want)
			}
			if key != msg {
				t.Errorf("IntegratorMsgAndKey(%q) key = %q, want %q", msg, key, msg)
			}
		}
	})

	t.Run("unknown message falls back", func(t *testing.T) {
		text, key := IntegratorMsgAndKey("redis: connection refused")
		if text != MsgIntegratorUnexpected {
			t.Errorf("text = %q, want %q", text, MsgIntegratorUnexpected)
		}
		if key != ErrAuthUnexpected.Error() {
			t.Errorf("key = %q, want %q", key, ErrAuthUnexpected.Error())
		}
	})
}

// TestAuthApiErrorKeysAreMapped proves every i18n key an integrator endpoint can
// answer with has curated English text, so a response never carries a fallback.
func TestAuthApiErrorKeysAreMapped(t *testing.T) {
	sentinels := []error{
		ErrAuthS2SApiKeyMissing,
		ErrAuthS2SApiKeyInvalid,
		ErrAuthS2SSignatureMissing,
		ErrAuthS2SSignatureInvalid,
		ErrAuthS2SRoomIdHeaderMissing,
		ErrFileInvalidRequest,
		ErrFileUploadInProgress,
		ErrFileInvalidDocumentLink,
		ErrFileTooLarge,
		ErrFileTypeNotAllowed,
		ErrFileUploadFailed,
		ErrFileConversionFailed,
		ErrRecordingInvalidRequest,
		ErrRecordingNotFound,
		ErrRequestedRecordingsNotFound,
		ErrRecordingFetchFailed,
		ErrRecordingUpdateFailed,
		ErrRecordingDeleteFailed,
		ErrRecordingMergeFailed,
		ErrRecordingMergeInProgress,
		ErrRecordingMergeMinRecordings,
		ErrRecordingDownloadTokenFailed,
		ErrArtifactInvalidRequest,
		ErrArtifactNotFound,
		ErrArtifactFetchFailed,
		ErrArtifactDeleteFailed,
		ErrArtifactNotDownloadable,
		ErrArtifactNoDownloadableFile,
		ErrArtifactDeleteNotAllowed,
		ErrArtifactDownloadTokenFailed,
		ErrAnalyticsFetchFailed,
		ErrAIAgentUserNotAllowed,
		ErrAIAgentSessionsFull,
		NoOnlineUserFound,
		NoOnlineAdminFound,
	}

	for _, sentinel := range sentinels {
		key := sentinel.Error()
		if _, ok := integratorMsgs[key]; !ok {
			t.Errorf("%q has no curated English text", key)
			continue
		}
		if text, gotKey := IntegratorMsgAndKey(key); text == MsgIntegratorUnexpected || gotKey != key {
			t.Errorf("%q = (%q, %q), want a curated pair", key, text, gotKey)
		}
	}
}
