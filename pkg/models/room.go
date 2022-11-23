package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type RoomInfo struct {
	Id                 int64
	RoomTitle          string `json:"room_title"`
	RoomId             string `json:"room_id"`
	Sid                string `json:"sid"`
	JoinedParticipants int64  `json:"joined_participants"`
	IsRunning          int    `json:"is_running"`
	IsRecording        int    `json:"is_recording"`
	RecorderId         string `json:"recorder_id"`
	IsActiveRTMP       int    `json:"is_active_rtmp"`
	NodeIdRTMP         string `json:"rtmp_node_id"`
	WebhookUrl         string `json:"webhook_url"`
	IsBreakoutRoom     int64  `json:"is_breakout_room"`
	ParentRoomId       string `json:"parent_room_id"`
	CreationTime       int64  `json:"creation_time"`
	Created            string
	Ended              string
}

type RoomModel struct {
	app *config.AppConfig
	db  *sql.DB
	ctx context.Context
}

func NewRoomModel() *RoomModel {
	return &RoomModel{
		app: config.AppCnf,
		db:  config.AppCnf.DB,
		ctx: context.Background(),
	}
}

func (rm *RoomModel) InsertOrUpdateRoomData(r *RoomInfo, update bool) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	query := "INSERT INTO " + rm.app.FormatDBTable("room_info") + " (room_title, roomId, sid, joined_participants, is_running, webhook_url, is_breakout_room, parent_room_id, creation_time, created) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE is_running = ?"

	if update {
		query = "UPDATE " + rm.app.FormatDBTable("room_info") + " SET room_title = ?, roomId = ?, sid = ?, joined_participants = ?, is_running = ?, webhook_url = ?, is_breakout_room = ?, parent_room_id = ?, creation_time = ? WHERE id = ?"
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, err
	}

	var values []interface{}
	if update {
		values = append(values, r.RoomTitle, r.RoomId, r.Sid, r.JoinedParticipants, r.IsRunning, r.WebhookUrl, r.IsBreakoutRoom, r.ParentRoomId, r.CreationTime, r.Id)
	} else {
		values = append(values, r.RoomTitle, r.RoomId, r.Sid, r.JoinedParticipants, r.IsRunning, r.WebhookUrl, r.IsBreakoutRoom, r.ParentRoomId, r.CreationTime, r.Created, 1)
	}

	res, err := stmt.ExecContext(ctx, values...)
	if err != nil {
		return 0, err
	}

	lastId, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	err = tx.Commit()
	if err != nil {
		return 0, err
	}

	err = stmt.Close()
	if err != nil {
		return 0, err
	}

	return lastId, nil
}

// UpdateRoomStatus will change the room status based on `is_running` value
// most of the place this method used to change status to close
func (rm *RoomModel) UpdateRoomStatus(r *RoomInfo) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var query string
	var args []interface{}

	switch {
	case len(r.RoomId) > 0:
		query = "UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_running = ? WHERE roomId = ?"
		args = append(args, r.IsRunning)

		if r.IsRunning == 0 {
			// when the meeting will be ended,
			// we can change sid to make sure that sid always unique
			// so, here we'll use sid-id
			query = "UPDATE " + rm.app.FormatDBTable("room_info") + " SET sid = CONCAT(sid, '-', id), is_running = ?, is_recording = 0, is_active_rtmp = 0, ended = ? WHERE roomId = ? AND is_running <> 0"

			args = append(args, r.Ended)
		}
		args = append(args, r.RoomId)

	default:
		query = "UPDATE " + rm.app.FormatDBTable("room_info") +
			" SET is_running = ? WHERE sid = ?"
		args = append(args, r.IsRunning)

		if r.IsRunning == 0 {
			// when the meeting will be ended,
			// we can change sid to make sure that sid always unique
			// so, here we'll use sid-id
			query = "UPDATE " + rm.app.FormatDBTable("room_info") +
				" SET sid = CONCAT(sid, '-', id), is_running = ?, is_recording = 0, is_active_rtmp = 0, ended = ? WHERE sid = ? AND is_running <> 0"

			args = append(args, r.Ended)
		}

		args = append(args, r.Sid)
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, err
	}

	res, err := stmt.ExecContext(ctx, args...)
	if err != nil {
		return 0, err
	}

	affectedId, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	err = tx.Commit()
	if err != nil {
		return 0, err
	}

	err = stmt.Close()
	if err != nil {
		return 0, err
	}

	return affectedId, nil
}

// UpdateRoomParticipants will increment or decrement number of Participants
func (rm *RoomModel) UpdateRoomParticipants(r *RoomInfo, operator string) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE "+rm.app.FormatDBTable("room_info")+
		" SET joined_participants = joined_participants "+operator+" 1 WHERE sid = ? OR sid = CONCAT(?, '-', id)")
	if err != nil {
		return 0, err
	}

	res, err := stmt.ExecContext(ctx, r.Sid, r.Sid)
	if err != nil {
		return 0, err
	}

	affectedId, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	err = tx.Commit()
	if err != nil {
		return 0, err
	}

	err = stmt.Close()
	if err != nil {
		return 0, err
	}

	return affectedId, nil
}

// UpdateNumParticipants will update total number of Participants
func (rm *RoomModel) UpdateNumParticipants(roomSid string, num int64) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE "+rm.app.FormatDBTable("room_info")+
		" SET joined_participants = ? WHERE sid = ? OR sid = CONCAT(?, '-', id)")
	if err != nil {
		return 0, err
	}

	res, err := stmt.ExecContext(ctx, num, roomSid, roomSid)
	if err != nil {
		return 0, err
	}

	affectedId, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	err = tx.Commit()
	if err != nil {
		return 0, err
	}

	err = stmt.Close()
	if err != nil {
		return 0, err
	}

	return affectedId, nil
}

func (rm *RoomModel) GetRoomInfo(roomId string, sid string, isRunning int) (*RoomInfo, string) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	var query *sql.Row

	switch {
	case len(roomId) > 0 && isRunning == 1 && len(sid) == 0:
		// for roomId + isRunning
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE roomId = ? AND is_running = 1", roomId)

	case len(sid) > 0 && isRunning == 1 && len(roomId) == 0:
		// for sid + isRunning
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE (sid = ? OR sid = CONCAT(?, '-', id)) AND is_running = 1", sid, sid)

	case len(roomId) > 0 && len(sid) > 0 && isRunning == 1:
		// for sid + roomId + isRunning
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE roomId = ? AND (sid = ? OR sid = CONCAT(?, '-', id)) AND is_running = 1", roomId, sid, sid)

	default:
		// for only sid
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE sid = ? OR sid = CONCAT(?, '-', id)", sid, sid)
	}

	var room RoomInfo
	var msg string
	err := query.Scan(&room.Id, &room.RoomTitle, &room.RoomId, &room.Sid, &room.JoinedParticipants, &room.IsRunning, &room.IsRecording, &room.IsActiveRTMP, &room.WebhookUrl, &room.IsBreakoutRoom, &room.ParentRoomId, &room.CreationTime)

	switch {
	case err == sql.ErrNoRows:
		msg = "no info found"
	case err != nil:
		msg = fmt.Sprintf("query error: %s", err.Error())
	default:
		msg = "success"
	}

	return &room, msg
}

func (rm *RoomModel) GetActiveRoomsInfo() ([]*plugnmeet.ActiveRoomInfo, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "select room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time from "+rm.app.FormatDBTable("room_info")+" where is_running = ?", 1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rooms []*plugnmeet.ActiveRoomInfo

	for rows.Next() {
		room := new(plugnmeet.ActiveRoomInfo)
		err := rows.Scan(&room.RoomTitle, &room.RoomId, &room.Sid, &room.JoinedParticipants, &room.IsRunning, &room.IsRecording, &room.IsActiveRtmp, &room.WebhookUrl, &room.IsBreakoutRoom, &room.ParentRoomId, &room.CreationTime)

		if err != nil {
			log.Errorln(err)
			continue
		}

		rooms = append(rooms, room)
	}

	if len(rooms) == 0 {
		return nil, errors.New("no active room found")
	}

	return rooms, nil
}
