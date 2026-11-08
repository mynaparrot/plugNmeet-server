package models

import (
	"errors"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// The empty-SID guard fires before any dependency is used, so a logger-only
// model is enough to pin the client-facing mapping.
func TestDispatchRecorderTaskRequiresRoomSid(t *testing.T) {
	m := &RecordingModel{logger: logrus.NewEntry(logrus.New())}

	err := m.DispatchRecorderTask(&plugnmeet.RecordingReq{Task: plugnmeet.RecordingTasks_STOP})
	if !errors.Is(err, config.ErrRecordingRoomSidRequired) {
		t.Fatalf("expected room-sid-required, got %v", err)
	}
}
