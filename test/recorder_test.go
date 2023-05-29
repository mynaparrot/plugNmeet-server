package test

import (
	"fmt"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
	"net/http"
	"testing"
	"time"
)

func test_recorderTasks(t *testing.T, rInfo *livekit.Room) string {
	rid := fmt.Sprintf("%s-%d", rInfo.Sid, time.Now().UnixMilli())
	rm := models.NewRoomModel()
	rf, _ := rm.GetRoomInfo(rInfo.Name, rInfo.Sid, 1)

	body := &plugnmeet.RecorderToPlugNmeet{
		From:        "recorder",
		Status:      true,
		Msg:         "success",
		RecordingId: rid,
		RoomTableId: rf.Id,
		RoomId:      rInfo.Name,
		RoomSid:     rInfo.Sid,
		RecorderId:  "node_01",
		FilePath:    fmt.Sprintf("%s/node_01/%s.mp4", config.AppCnf.RecorderInfo.RecordingFilesPath, rid),
		FileSize:    10,
	}

	tests := []struct {
		task plugnmeet.RecordingTasks
	}{
		{
			task: plugnmeet.RecordingTasks_START_RECORDING,
		},
		{
			task: plugnmeet.RecordingTasks_STOP_RECORDING,
		},
		{
			task: plugnmeet.RecordingTasks_START_RTMP,
		},
		{
			task: plugnmeet.RecordingTasks_STOP_RTMP,
		},
		{
			task: plugnmeet.RecordingTasks_RECORDING_PROCEEDED,
		},
	}

	for _, tt := range tests {
		body.Task = tt.task
		marshal, err := proto.Marshal(body)
		if err != nil {
			t.Error(err)
		}

		req := prepareByteReq(http.MethodPost, "/auth/recorder/notify", marshal)
		t.Run(tt.task.String(), func(t *testing.T) {
			performCommonStatusReq(t, req)
		})
	}

	return rid
}
