package dbmodels

import (
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type RoomInfo struct {
	ID                 uint64    `gorm:"column:id;type:int(11);primarykey;autoIncrement"`
	RoomTitle          string    `gorm:"column:room_title;type:varchar(255);not null;default:''"`
	RoomId             string    `gorm:"column:roomId;type:varchar(64);not null;index:idx_room_id"`
	Sid                string    `gorm:"column:sid;type:varchar(64);not null;uniqueIndex:sid"`
	JoinedParticipants int64     `gorm:"column:joined_participants;type:int(10);not null;default:0"`
	IsRunning          int       `gorm:"column:is_running;type:int(1);not null;default:0;index:idx_room_id"`
	IsRecording        int       `gorm:"column:is_recording;type:int(1);not null;default:0"`
	RecorderID         string    `gorm:"column:recorder_id;type:varchar(36);not null;default:''"`
	IsActiveRtmp       int       `gorm:"column:is_active_rtmp;type:int(1);not null;default:0"`
	RtmpNodeID         string    `gorm:"column:rtmp_node_id;type:varchar(36);not null;default:''"`
	WebhookUrl         string    `gorm:"column:webhook_url;type:varchar(255);not null;default:''"`
	IsBreakoutRoom     int       `gorm:"column:is_breakout_room;type:int(1);not null;default:0"`
	ParentRoomID       string    `gorm:"column:parent_room_id;type:varchar(64);not null;default:''"`
	CreationTime       int64     `gorm:"column:creation_time;type:int(10);not null;autoCreateTime"`
	Created            time.Time `gorm:"column:created;type:datetime;not null;default:current_timestamp()"`
	Ended              time.Time `gorm:"column:ended;type:datetime;not null;default:'0000-00-00 00:00:00'"`
	Modified           time.Time `gorm:"column:modified;type:datetime;not null;default:'0000-00-00 00:00:00';autoUpdateTime"`
}

func (t *RoomInfo) TableName() string {
	return config.FormatDBTable("room_info")
}
