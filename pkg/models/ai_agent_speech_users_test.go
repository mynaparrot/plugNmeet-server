package models

import (
	"reflect"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func speechTestUsers() []*plugnmeet.NatsKvUserInfo {
	return []*plugnmeet.NatsKvUserInfo{
		{UserId: "user-1", Name: "Alice"},
		{UserId: "user-2", Name: "Bob"},
		{UserId: config.RecorderBot, Name: "Recorder"},
		{UserId: config.RtmpBot, Name: "RTMP"},
		{UserId: config.AgentUserUserIdPrefix + "transcription", Name: "Agent"},
	}
}

func TestResolveSpeechUsers(t *testing.T) {
	users := speechTestUsers()

	cases := []struct {
		name    string
		allowed []string
		users   []*plugnmeet.NatsKvUserInfo
		want    []string
	}{
		{
			name:    "everyone resolves to the online humans",
			allowed: []string{"everyone"},
			users:   users,
			want:    []string{"user-1", "user-2"},
		},
		{
			name:    "all alias is case-insensitive",
			allowed: []string{" ALL "},
			users:   users,
			want:    []string{"user-1", "user-2"},
		},
		{
			name:    "names and ids are matched",
			allowed: []string{" alice ", "user-2"},
			users:   users,
			want:    []string{"user-1", "user-2"},
		},
		{
			name:    "internal users are skipped",
			allowed: []string{config.RecorderBot, config.RtmpBot, config.AgentUserUserIdPrefix + "transcription", "Bob"},
			users:   users,
			want:    []string{"user-2"},
		},
		{
			name:    "unknown participants are dropped",
			allowed: []string{"nobody"},
			users:   users,
			want:    []string{},
		},
		{
			name:    "duplicates are removed",
			allowed: []string{"Alice", "user-1"},
			users:   users,
			want:    []string{"user-1"},
		},
		{
			name:    "raw ids are kept without an online list",
			allowed: []string{"user-9"},
			want:    []string{"user-9"},
		},
		{
			name:    "everyone needs the online list",
			allowed: []string{"everyone"},
			want:    []string{},
		},
		{
			name:    "empty selection stays empty",
			allowed: nil,
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSpeechUsers(tc.allowed, tc.users)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolveSpeechUsers(%v) = %v, want %v", tc.allowed, got, tc.want)
			}
		})
	}
}
