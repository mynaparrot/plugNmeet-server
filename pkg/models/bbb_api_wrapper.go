package models

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

type BBBApiWrapperModel struct {
	app           *config.AppConfig
	db            *sql.DB
	roomService   *RoomService
	ctx           context.Context
	recordingAuth *AuthRecording
}

func NewBBBApiWrapperModel() *BBBApiWrapperModel {
	return &BBBApiWrapperModel{
		app:           config.AppCnf,
		db:            config.AppCnf.DB,
		roomService:   NewRoomService(),
		ctx:           context.Background(),
		recordingAuth: NewRecordingAuth(),
	}
}

func (m *BBBApiWrapperModel) GetRecordings(host string, r *bbbapiwrapper.GetRecordingsReq) ([]*bbbapiwrapper.RecordingInfo, *bbbapiwrapper.Pagination, error) {
	db := m.db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	var query []string
	var args []interface{}
	oriIds := make(map[string]string)
	if r.Limit == 0 {
		// let's make it 50 for BBB as not all plugin still support pagination
		r.Limit = 50
	}

	query = append(query, "SELECT a.record_id, a.room_id, a.room_sid, a.file_path, a.size, a.published, b.room_title, b.joined_participants, b.created, b.ended")

	if r.RecordID != "" {
		rIds := strings.Split(r.RecordID, ",")
		q := "FROM " + m.app.FormatDBTable("recordings") + " AS a LEFT JOIN " + m.app.FormatDBTable("room_info") + " AS b ON a.room_sid = b.sid WHERE a.record_id IN (?" + strings.Repeat(",?", len(rIds)-1) + ")"

		query = append(query, q, "ORDER BY a.id DESC LIMIT ?,?")
		for _, rd := range rIds {
			args = append(args, rd)
		}
		args = append(args, r.Offset)
		args = append(args, r.Limit)

	} else if r.MeetingID != "" {
		mIds := strings.Split(r.MeetingID, ",")
		q := "FROM " + m.app.FormatDBTable("recordings") + " AS a LEFT JOIN " + m.app.FormatDBTable("room_info") + " AS b ON a.room_sid = b.sid WHERE a.room_id IN (?" + strings.Repeat(",?", len(mIds)-1) + ")"

		query = append(query, q, "ORDER BY a.id DESC LIMIT ?,?")
		for _, rd := range mIds {
			fId := bbbapiwrapper.CheckMeetingIdToMatchFormat(rd)
			oriIds[fId] = rd
			args = append(args, fId)
		}
		args = append(args, r.Offset)
		args = append(args, r.Limit)

	} else {
		q := "FROM " + m.app.FormatDBTable("recordings") + " AS a LEFT JOIN " + m.app.FormatDBTable("room_info") + " AS b ON a.room_sid = b.sid"

		query = append(query, q, "ORDER BY a.id DESC LIMIT ?,?")
		args = append(args, r.Offset)
		args = append(args, r.Limit)
	}

	rows, err := db.QueryContext(ctx, strings.Join(query, " "), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var recordings []*bbbapiwrapper.RecordingInfo
	for rows.Next() {
		var recording bbbapiwrapper.RecordingInfo
		var rSid sql.NullString
		var meetingId, path, created, ended string
		var size float64
		var participants int64

		err = rows.Scan(&recording.RecordID, &meetingId, &rSid, &path, &size, &recording.Published, &recording.Name, &participants, &created, &ended)
		if err != nil {
			log.Errorln(err)
			continue
		}

		if oriIds[meetingId] != "" {
			recording.MeetingID = oriIds[meetingId]
		} else {
			recording.MeetingID = meetingId
		}
		recording.InternalMeetingID = rSid.String

		// for path, let's create a download link directly
		url, err := m.createPlayBackURL(host, path)
		if err != nil {
			log.Errorln(err)
			continue
		}
		recording.Playback.PlayBackFormat = []bbbapiwrapper.PlayBackFormat{
			{
				Type: "presentation",
				URL:  url,
			},
		}

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

	// get total recordings
	aa := args[:len(args)-2]
	qq := fmt.Sprintf("SELECT COUNT(*) AS total %s", query[1])
	row := db.QueryRowContext(ctx, qq, aa...)
	var total uint64
	err = row.Scan(&total)
	if err != nil {
		log.Errorln(err)
	}

	pagination := &bbbapiwrapper.Pagination{
		Pageable: &bbbapiwrapper.PaginationPageable{
			Offset: r.Offset,
			Limit:  r.Limit,
		},
		TotalElements: total,
	}
	if total == 0 {
		pagination.Empty = true
	}

	return recordings, pagination, nil
}

func (m *BBBApiWrapperModel) createPlayBackURL(host, path string) (string, error) {
	token, err := m.recordingAuth.CreateTokenForDownload(path)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/download/recording/%s", host, token)
	return url, nil
}
