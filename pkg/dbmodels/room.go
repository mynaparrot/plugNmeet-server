package dbmodels

import (
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type RoomInfo struct {
	ID                 uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	RoomTitle          string    `gorm:"column:room_title;NOT NULL"`
	RoomId             string    `gorm:"column:roomId;NOT NULL"`
	Sid                string    `gorm:"column:sid;unique;NOT NULL"`
	JoinedParticipants int64     `gorm:"column:joined_participants;default:0;NOT NULL"`
	IsRunning          int       `gorm:"column:is_running;default:0;NOT NULL"`
	IsRecording        int       `gorm:"column:is_recording;default:0;NOT NULL"`
	RecorderID         string    `gorm:"column:recorder_id;NOT NULL"`
	IsActiveRtmp       int       `gorm:"column:is_active_rtmp;default:0;NOT NULL"`
	RtmpNodeID         string    `gorm:"column:rtmp_node_id;NOT NULL"`
	WebhookUrl         string    `gorm:"column:webhook_url;NOT NULL"`
	IsBreakoutRoom     int       `gorm:"column:is_breakout_room;default:0;NOT NULL"`
	ParentRoomID       string    `gorm:"column:parent_room_id;NOT NULL"`
	CreationTime       int64     `gorm:"column:creation_time;autoCreateTime;NOT NULL"`
	Created            time.Time `gorm:"column:created;autoCreateTime;NOT NULL"`
	Ended              time.Time `gorm:"column:ended;default:0000-00-00 00:00:00;NOT NULL"`
	Modified           time.Time `gorm:"column:modified;autoUpdateTime;NOT NULL"`
}

func (m *RoomInfo) TableName() string {
	return config.FormatDBTable("room_info")
}
