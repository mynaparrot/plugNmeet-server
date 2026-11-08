package controllers

import (
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// TestAuthApiFileMessagesArePlainEnglish guards the integrator contract: the Auth API
// file endpoint answers with curated English text, never with the client i18n keys
// (room.errors.*, file.errors.*) that browsers resolve via t().
func TestAuthApiFileMessagesArePlainEnglish(t *testing.T) {
	messages := map[string]string{
		"room id required":      config.MsgRoomIdRequired,
		"room not active":       config.MsgFileRoomNotActive,
		"invalid request":       config.MsgFileInvalidRequest,
		"upload in progress":    config.MsgFileUploadInProgress,
		"invalid document link": config.MsgFileInvalidDocumentLink,
		"too large":             config.MsgFileTooLarge,
		"type not allowed":      config.MsgFileTypeNotAllowed,
		"upload failed":         config.MsgFileUploadFailed,
		"conversion failed":     config.MsgFileConversionFailed,
	}

	for name, msg := range messages {
		if !strings.Contains(msg, " ") {
			t.Errorf("%s: %q is not plain English", name, msg)
		}
		if strings.Contains(msg, "errors.") || strings.HasPrefix(msg, "notifications.") {
			t.Errorf("%s: %q looks like an i18n key", name, msg)
		}
	}

	// the room model's messages are client i18n keys and must never reach integrators
	for _, key := range []error{config.ErrRoomNotActive, config.ErrRoomNotFound, config.ErrRoomUnexpectedError} {
		if config.MsgFileRoomNotActive == key.Error() {
			t.Errorf("room i18n key leaked into the Auth API file message: %q", config.MsgFileRoomNotActive)
		}
	}
}
