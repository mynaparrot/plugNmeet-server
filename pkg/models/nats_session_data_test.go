package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestBuildSessionDataSaveAckHeader(t *testing.T) {
	key := "page-1"
	targetRoom := "breakout-1"
	requestId := "req-42"

	tests := []struct {
		name       string
		header     *plugnmeet.SessionDataHeader
		success    bool
		wantTarget string
	}{
		{
			name: "request room save",
			header: &plugnmeet.SessionDataHeader{
				DataType:  plugnmeet.SessionDataType_SESSION_DATA_TYPE_WHITEBOARD,
				Key:       &key,
				RequestId: &requestId,
			},
			success: true,
		},
		{
			name: "breakout seeding echoes target room",
			header: &plugnmeet.SessionDataHeader{
				DataType:     plugnmeet.SessionDataType_SESSION_DATA_TYPE_NOTEPAD,
				Key:          &key,
				TargetRoomId: &targetRoom,
				RequestId:    &requestId,
			},
			success:    false,
			wantTarget: targetRoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ack := buildSessionDataSaveAckHeader(tt.header, tt.success)

			if ack.GetDataType() != tt.header.GetDataType() {
				t.Errorf("data_type = %v, want %v", ack.GetDataType(), tt.header.GetDataType())
			}
			if ack.GetKey() != key {
				t.Errorf("key = %q, want %q", ack.GetKey(), key)
			}
			if ack.GetRequestId() != requestId {
				t.Errorf("request_id = %q, want %q", ack.GetRequestId(), requestId)
			}
			if ack.GetTargetRoomId() != tt.wantTarget {
				t.Errorf("target_room_id = %q, want %q", ack.GetTargetRoomId(), tt.wantTarget)
			}
			// success must always be present, otherwise the client cannot tell
			// a failure ack from a missing field.
			if ack.Success == nil || ack.GetSuccess() != tt.success {
				t.Errorf("success = %v, want %v", ack.Success, tt.success)
			}

			// the ack travels as protojson, same convention as fetch responses
			msg, err := protojson.Marshal(ack)
			if err != nil {
				t.Fatalf("marshal ack: %v", err)
			}
			got := new(plugnmeet.SessionDataHeader)
			if err := protojson.Unmarshal(msg, got); err != nil {
				t.Fatalf("unmarshal ack %s: %v", msg, err)
			}
			if got.GetRequestId() != requestId || got.Success == nil || got.GetSuccess() != tt.success {
				t.Errorf("round trip lost ack fields: %s", msg)
			}
		})
	}
}
