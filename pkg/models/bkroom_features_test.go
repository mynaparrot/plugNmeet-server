package models

import "testing"

func TestIsBreakoutChildRoomId(t *testing.T) {
	tests := []struct {
		name     string
		roomId   string
		parentId string
		want     bool
	}{
		{name: "direct child", roomId: "room-1", parentId: "room", want: true},
		{name: "multi digit index", roomId: "room-12", parentId: "room", want: true},
		{name: "hyphenated parent", roomId: "room-1-2", parentId: "room-1", want: true},
		{name: "nested suffix is not a number", roomId: "room-1-2", parentId: "room", want: false},
		{name: "same room", roomId: "room", parentId: "room", want: false},
		{name: "sibling parent", roomId: "room-1-2", parentId: "other", want: false},
		{name: "empty parent", roomId: "room-1", parentId: "", want: false},
		{name: "empty room id", roomId: "", parentId: "room", want: false},
		{name: "zero index", roomId: "room-0", parentId: "room", want: false},
		{name: "empty index", roomId: "room-", parentId: "room", want: false},
		{name: "negative index", roomId: "room--1", parentId: "room", want: false},
		{name: "non numeric index", roomId: "room-abc", parentId: "room", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBreakoutChildRoomId(tt.roomId, tt.parentId); got != tt.want {
				t.Fatalf("isBreakoutChildRoomId(%q, %q) = %v, want %v", tt.roomId, tt.parentId, got, tt.want)
			}
		})
	}
}
