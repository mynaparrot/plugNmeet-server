package models

import (
	"context"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

// TestAdminAISessionPreCreate locks in that an admin's own 1-on-1 room is only
// prepared when the room runs auto per-user sessions and the admin is explicitly
// pre-authorized: an observer admin must not hold a concurrent session slot.
func TestAdminAISessionPreCreate(t *testing.T) {
	tests := []struct {
		name     string
		features *plugnmeet.InsightsAIAgentFeatures
		userId   string
		want     bool
	}{
		{name: "nil features", features: nil, userId: "admin-1", want: false},
		{name: "normal room", features: agentFeatures(true, modeNormalRoom, controlledConfig("admin-1")), userId: "admin-1", want: false},
		{name: "open per user is never explicit", features: agentFeatures(true, modeOpenPerUse, openPerUserConfig(3)), userId: "admin-1", want: false},
		{name: "controlled single user pre-authorizes that user", features: agentFeatures(true, modeControlled, controlledConfig("admin-1")), userId: "admin-1", want: true},
		{name: "controlled single user omits the admin", features: agentFeatures(true, modeControlled, controlledConfig("learner-1")), userId: "admin-1", want: false},
		{name: "controlled roster lists the admin", features: agentFeatures(true, modeControlled, controlledConfig("learner-1", "admin-1")), userId: "admin-1", want: true},
		{name: "controlled roster omits the admin", features: agentFeatures(true, modeControlled, controlledConfig("learner-1", "learner-2")), userId: "admin-1", want: false},
		{name: "agent not allowed", features: agentFeatures(false, modeControlled, controlledConfig("learner-1", "admin-1")), userId: "admin-1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminAISessionPreCreate(tt.features, tt.userId); got != tt.want {
				t.Fatalf("adminAISessionPreCreate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPerUserAISessionRoomAdminStaysInMainRoom locks in that an observer admin,
// who is not part of the pre-authorized roster, joins the main room without any
// room being prepared. The nil breakout model would panic if it were touched.
func TestPerUserAISessionRoomAdminStaysInMainRoom(t *testing.T) {
	meta := agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("learner-1")))
	req := &plugnmeet.GenerateTokenReq{
		RoomId:   "room-1",
		UserInfo: &plugnmeet.UserInfo{UserId: "admin-1", Name: "Admin", IsAdmin: true},
	}

	roomId, err := new(UserModel).perUserAISessionRoom(context.Background(), req, meta, nil, logrus.NewEntry(logrus.New()))
	if err != nil {
		t.Fatalf("perUserAISessionRoom() error = %v, want nil", err)
	}
	if roomId != "" {
		t.Fatalf("perUserAISessionRoom() = %q, want an empty room id", roomId)
	}
}

// TestLockAIAgentRoomWorkspace locks in that every participant of a 1-on-1 AI
// agent room ends up without presenter rights, without the waiting room, and with
// a locked whiteboard and notepad, even when the metadata already carried other
// lock settings, as JoinBreakoutRoom hands the parent room metadata over. Only
// the named participant keeps the microphone, as an observer joins to watch.
func TestLockAIAgentRoomWorkspace(t *testing.T) {
	oneOnOneRoom := agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("learner-1")))
	oneOnOneRoom.IsBreakoutRoom = true

	carriedLocks := func(isAdmin bool) *plugnmeet.UserMetadata {
		return &plugnmeet.UserMetadata{
			IsAdmin:         isAdmin,
			IsPresenter:     true,
			WaitForApproval: true,
			LockSettings: &plugnmeet.LockSettings{
				LockWhiteboard:    new(false),
				LockSharedNotepad: new(false),
			},
		}
	}
	newRequest := func(userId string, md *plugnmeet.UserMetadata) *plugnmeet.GenerateTokenReq {
		return &plugnmeet.GenerateTokenReq{
			UserInfo: &plugnmeet.UserInfo{UserId: userId, IsAdmin: md.GetIsAdmin(), UserMetadata: md},
		}
	}

	tests := []struct {
		name string
		req  *plugnmeet.GenerateTokenReq
		// wantMicLocked is true for an observer, who is not a named participant
		wantMicLocked bool
	}{
		{name: "observing admin carrying the parent room locks", req: newRequest("admin-1", carriedLocks(true)), wantMicLocked: true},
		{name: "participant carrying the parent room locks", req: newRequest("learner-1", carriedLocks(false))},
		{name: "participant without lock settings", req: newRequest("learner-1", new(plugnmeet.UserMetadata))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lockAIAgentRoomWorkspace(oneOnOneRoom, tt.req)

			md := tt.req.GetUserInfo().GetUserMetadata()
			if md.GetIsPresenter() {
				t.Error("is_presenter = true, want false")
			}
			if md.GetWaitForApproval() {
				t.Error("wait_for_approval = true, want false")
			}
			locks := md.GetLockSettings()
			if locks == nil {
				t.Fatal("lock_settings = nil, want them assigned")
			}
			if locks.LockWhiteboard == nil || !*locks.LockWhiteboard {
				t.Errorf("lock_whiteboard = %v, want an explicit true", locks.GetLockWhiteboard())
			}
			if locks.LockSharedNotepad == nil || !*locks.LockSharedNotepad {
				t.Errorf("lock_shared_notepad = %v, want an explicit true", locks.GetLockSharedNotepad())
			}
			if got := locks.GetLockMicrophone(); got != tt.wantMicLocked {
				t.Errorf("lock_microphone = %v, want %v", got, tt.wantMicLocked)
			}
		})
	}

	// a room without a roster has no observer, so everyone keeps the microphone
	t.Run("open per-user room keeps the microphone", func(t *testing.T) {
		room := agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(3)))
		room.IsBreakoutRoom = true
		req := newRequest("admin-1", new(plugnmeet.UserMetadata))

		lockAIAgentRoomWorkspace(room, req)

		if req.GetUserInfo().GetUserMetadata().GetLockSettings().GetLockMicrophone() {
			t.Error("lock_microphone = true, want the microphone kept")
		}
	})

	// the parent lobby keeps the participant's own settings
	t.Run("any other room is left untouched", func(t *testing.T) {
		req := newRequest("learner-1", carriedLocks(false))
		lockAIAgentRoomWorkspace(agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(3))), req)

		md := req.GetUserInfo().GetUserMetadata()
		if !md.GetIsPresenter() || !md.GetWaitForApproval() {
			t.Errorf("is_presenter = %v, wait_for_approval = %v, want both untouched", md.GetIsPresenter(), md.GetWaitForApproval())
		}
		if md.GetLockSettings().GetLockWhiteboard() || md.GetLockSettings().GetLockSharedNotepad() || md.GetLockSettings().GetLockMicrophone() {
			t.Error("lock settings changed, want them untouched")
		}
	})
}
