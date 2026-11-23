package models

import (
	"context"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// fakeWorkspaceNats keeps the room metadata in memory and counts broadcasts.
type fakeWorkspaceNats struct {
	meta      *plugnmeet.RoomMetadata
	broadcast int
}

func (f *fakeWorkspaceNats) GetRoomMetadataStruct(string) (*plugnmeet.RoomMetadata, error) {
	return f.meta, nil
}

func (f *fakeWorkspaceNats) UpdateAndBroadcastRoomMetadata(string, interface{}) error {
	f.broadcast++
	return nil
}

func roomWithWorkspaceSurfaces() *plugnmeet.RoomMetadata {
	unlocked := false

	return &plugnmeet.RoomMetadata{
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			WhiteboardFeatures:    &plugnmeet.WhiteboardFeatures{IsAllow: true},
			SharedNotePadFeatures: &plugnmeet.SharedNotePadFeatures{IsAllow: true},
		},
		DefaultLockSettings: &plugnmeet.LockSettings{
			LockWhiteboard:    &unlocked,
			LockSharedNotepad: &unlocked,
		},
	}
}

func TestEnsureWhiteboardVisibleKeepsTheBoardLocked(t *testing.T) {
	meta := roomWithWorkspaceSurfaces()
	meta.RoomFeatures.SharedNotePadFeatures.Visible = true
	nats := &fakeWorkspaceNats{meta: meta}

	if err := (aiAgentWhiteboardBackend{nats: nats}).EnsureWhiteboardVisible(context.Background(), "room-1"); err != nil {
		t.Fatalf("EnsureWhiteboardVisible: %v", err)
	}

	if !meta.RoomFeatures.WhiteboardFeatures.Visible {
		t.Error("the whiteboard must become visible")
	}
	// The notepad docks beside the board, so it stays visible side-by-side.
	if !meta.RoomFeatures.SharedNotePadFeatures.Visible {
		t.Error("the shared notepad must stay visible when the whiteboard opens")
	}
	// Opening the board must never hand the pen to the participant.
	if meta.DefaultLockSettings.GetLockWhiteboard() {
		t.Error("opening the whiteboard must not change its lock")
	}
	if meta.DefaultLockSettings.LockWhiteboard == nil || *meta.DefaultLockSettings.LockWhiteboard {
		t.Error("the whiteboard lock value must stay untouched")
	}
	if nats.broadcast != 1 {
		t.Errorf("broadcast = %d, want 1", nats.broadcast)
	}

	// A second call has nothing left to change.
	if err := (aiAgentWhiteboardBackend{nats: nats}).EnsureWhiteboardVisible(context.Background(), "room-1"); err != nil {
		t.Fatalf("EnsureWhiteboardVisible: %v", err)
	}
	if nats.broadcast != 1 {
		t.Errorf("an unchanged board must not broadcast again: %d", nats.broadcast)
	}
}

func TestEnsureNotepadActiveKeepsTheNotepadLocked(t *testing.T) {
	meta := roomWithWorkspaceSurfaces()
	nats := &fakeWorkspaceNats{meta: meta}

	if err := (aiAgentNotepadBackend{nats: nats}).EnsureNotepadActive(context.Background(), "room-1"); err != nil {
		t.Fatalf("EnsureNotepadActive: %v", err)
	}

	np := meta.RoomFeatures.SharedNotePadFeatures
	if np.NotePadId == "" {
		t.Error("the shared notepad needs an id")
	}
	// A normal room's participants follow the presenter's visibility.
	if !np.IsActive || !np.Visible {
		t.Errorf("the shared notepad must be active and visible: %+v", np)
	}
	if meta.DefaultLockSettings.GetLockSharedNotepad() {
		t.Error("opening the shared notepad must not change its lock")
	}
	if meta.DefaultLockSettings.LockSharedNotepad == nil || *meta.DefaultLockSettings.LockSharedNotepad {
		t.Error("the shared notepad lock value must stay untouched")
	}
	if nats.broadcast != 1 {
		t.Errorf("broadcast = %d, want 1", nats.broadcast)
	}
}

func TestEnsureNotepadActiveSkipsVisibilityInAISessionRoom(t *testing.T) {
	meta := roomWithWorkspaceSurfaces()
	meta.IsBreakoutRoom = true
	meta.RoomFeatures.InsightsFeatures = &plugnmeet.InsightsFeatures{
		AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{IsAllow: true},
	}
	nats := &fakeWorkspaceNats{meta: meta}

	if err := (aiAgentNotepadBackend{nats: nats}).EnsureNotepadActive(context.Background(), "room-1"); err != nil {
		t.Fatalf("EnsureNotepadActive: %v", err)
	}

	np := meta.RoomFeatures.SharedNotePadFeatures
	if !np.IsActive {
		t.Error("the shared notepad must become active")
	}
	// The 1-on-1 session room opens the pad locally instead of broadcasting it.
	if np.Visible {
		t.Error("a 1-on-1 ai session room must not broadcast the notepad visibility")
	}
	if nats.broadcast != 1 {
		t.Errorf("broadcast = %d, want 1", nats.broadcast)
	}
}

func TestAIAgentControlsWorkspace(t *testing.T) {
	oneOnOne := &plugnmeet.RoomMetadata{
		IsBreakoutRoom: true,
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{
				AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{IsAllow: true},
			},
		},
	}

	tests := []struct {
		name     string
		model    *RoomModel
		roomMeta *plugnmeet.RoomMetadata
		want     bool
	}{
		{
			name:     "1-on-1 ai session room",
			model:    &RoomModel{},
			roomMeta: oneOnOne,
			want:     true,
		},
		{
			name:     "a normal room without an agent",
			model:    &RoomModel{},
			roomMeta: &plugnmeet.RoomMetadata{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.model.aiAgentControlsWorkspace("room-1", tt.roomMeta); got != tt.want {
				t.Fatalf("aiAgentControlsWorkspace() = %v, want %v", got, tt.want)
			}
		})
	}
}
