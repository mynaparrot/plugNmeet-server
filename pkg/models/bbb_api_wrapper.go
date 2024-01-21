package models

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"strings"
	"time"
)

type BBBApiWrapperModel struct {
	app         *config.AppConfig
	db          *sql.DB
	roomService *RoomService
	ctx         context.Context
}

func NewBBBApiWrapperModel() *BBBApiWrapperModel {
	return &BBBApiWrapperModel{
		app:         config.AppCnf,
		db:          config.AppCnf.DB,
		roomService: NewRoomService(),
		ctx:         context.Background(),
	}
}

func (m *BBBApiWrapperModel) GetRecordings(r *bbbapiwrapper.GetRecordingsReq) ([]*bbbapiwrapper.RecordingInfo, error) {
	db := m.db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	var query string
	var args []interface{}
	if r.Limit == 0 {
		r.Limit = 20
	}

	if r.MeetingID != "" {
		mIds := strings.Split(r.MeetingID, ",")
		query = "SELECT a.record_id, a.room_id, a.room_sid, a.file_path, a.size, a.published, b.room_title, b.joined_participants, b.created, b.ended FROM " + m.app.FormatDBTable("recordings") + " AS a LEFT JOIN " + m.app.FormatDBTable("room_info") + " AS b ON a.room_sid = b.sid WHERE room_id IN (?" + strings.Repeat(",?", len(mIds)-1) + ") ORDER BY a.id DESC LIMIT ?,?"

		for _, rd := range mIds {
			args = append(args, rd)
		}
		args = append(args, r.Offset)
		args = append(args, r.Limit)

	} else if r.RecordID != "" {
		rIds := strings.Split(r.RecordID, ",")
		query = "SELECT a.record_id, a.room_id, a.room_sid, a.file_path, a.size, a.published, b.room_title, b.joined_participants, b.created, b.ended FROM " + m.app.FormatDBTable("recordings") + " AS a LEFT JOIN " + m.app.FormatDBTable("room_info") + " AS b ON a.room_sid = b.sid WHERE room_id IN (?" + strings.Repeat(",?", len(rIds)-1) + ") ORDER BY a.id DESC LIMIT ?,?"

		for _, rd := range rIds {
			args = append(args, rd)
		}
		args = append(args, r.Offset)
		args = append(args, r.Limit)

	} else {
		query = "SELECT a.record_id, a.room_id, a.room_sid, a.file_path, a.size, a.published, b.room_title, b.joined_participants, b.created, b.ended FROM " + m.app.FormatDBTable("recordings") + " AS a LEFT JOIN " + m.app.FormatDBTable("room_info") + " AS b ON a.room_sid = b.sid ORDER BY a.id DESC LIMIT ?,?"
		args = append(args, r.Offset)
		args = append(args, r.Limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recordings []*bbbapiwrapper.RecordingInfo
	for rows.Next() {
		var recording bbbapiwrapper.RecordingInfo
		var rSid sql.NullString
		var path, created, ended string
		var size float64
		var participants int64

		err = rows.Scan(&recording.RecordID, &recording.MeetingID, &rSid, &path, &size, &recording.Published, &recording.Name, &participants, &created, &ended)
		if err != nil {
			fmt.Println(err)
			continue
		}
		recording.InternalMeetingID = rSid.String
		// for path let's create a download link directly
		//for path

		if date, err := time.Parse("2006-01-02 15:04:05", created); err == nil {
			recording.StartTime = date.UnixMilli()
		}
		if date, err := time.Parse("2006-01-02 15:04:05", ended); err == nil {
			recording.EndTime = date.UnixMilli()
		}
		if recording.Published {
			recording.State = "published"
		}
		if size > 0 {
			recording.RawSize = int64(size * 1000000)
			recording.Size = recording.RawSize
		}
		if participants > 0 {
			recording.Participants = uint64(participants)
		}

		recordings = append(recordings, &recording)
	}

	return recordings, nil
}
