package dbmodels

import (
	"time"
)

type RoomInfo struct {
	ID                 int64     `gorm:"column:id;primaryKey;AUTO_INCREMENT"`
	RoomTitle          string    `gorm:"column:room_title;NOT NULL"`
	RoomId             string    `gorm:"column:roomId;NOT NULL"`
	Sid                string    `gorm:"column:sid;NOT NULL"`
	JoinedParticipants int64     `gorm:"column:joined_participants;default:0;NOT NULL"`
	IsRunning          int       `gorm:"column:is_running;default:0;NOT NULL"`
	IsRecording        int64     `gorm:"column:is_recording;default:0;NOT NULL"`
	RecorderID         string    `gorm:"column:recorder_id;NOT NULL"`
	IsActiveRtmp       int64     `gorm:"column:is_active_rtmp;default:0;NOT NULL"`
	RtmpNodeID         string    `gorm:"column:rtmp_node_id;NOT NULL"`
	WebhookUrl         string    `gorm:"column:webhook_url;NOT NULL"`
	IsBreakoutRoom     int64     `gorm:"column:is_breakout_room;default:0;NOT NULL"`
	ParentRoomID       string    `gorm:"column:parent_room_id;NOT NULL"`
	CreationTime       int64     `gorm:"column:creation_time;default:0;NOT NULL"`
	Created            time.Time `gorm:"column:created;default:CURRENT_TIMESTAMP;NOT NULL"`
	Ended              time.Time `gorm:"column:ended;default:0000-00-00 00:00:00;NOT NULL"`
	Modified           time.Time `gorm:"column:modified;default:0000-00-00 00:00:00;NOT NULL"`
}

func (m *RoomInfo) TableName() string {
	return "pnm_room_info"
}
