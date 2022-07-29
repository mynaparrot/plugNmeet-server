package models

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mynaparrot/plugNmeet/pkg/config"
	"time"
)

type RoomInfo struct {
	Id                 int
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
	Ended              string
}

type roomModel struct {
	app *config.AppConfig
	db  *sql.DB
	ctx context.Context
}

func NewRoomModel() *roomModel {
	return &roomModel{
		app: config.AppCnf,
		db:  config.AppCnf.DB,
		ctx: context.Background(),
	}
}

func (rm *roomModel) InsertOrUpdateRoomData(r *RoomInfo, update bool) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	query := "INSERT INTO " + rm.app.FormatDBTable("room_info") + " (room_title, roomId, sid, joined_participants, is_running, webhook_url, is_breakout_room, parent_room_id, creation_time) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE is_running = ?"

	if update {
		query = "UPDATE " + rm.app.FormatDBTable("room_info") + " SET room_title = ?, roomId = ?, sid = ?, joined_participants = ?, is_running = ?, webhook_url = ?, is_breakout_room = ?, parent_room_id = ?, creation_time = ? WHERE id = ?"
	}

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, err
	}

	lastVal := 1
	if update {
		lastVal = r.Id
	}

	res, err := stmt.ExecContext(ctx, r.RoomTitle, r.RoomId, r.Sid, r.JoinedParticipants, r.IsRunning, r.WebhookUrl, r.IsBreakoutRoom, r.ParentRoomId, r.CreationTime, lastVal)
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

func (rm *roomModel) UpdateRoomStatus(r *RoomInfo) (int64, error) {
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
		query = "UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_running = ?, ended = ? WHERE roomId = ?"
		args = append(args, r.IsRunning)
		args = append(args, r.Ended)
		args = append(args, r.RoomId)
	default:
		query = "UPDATE " + rm.app.FormatDBTable("room_info") +
			" SET is_running = ?, ended = ? WHERE sid = ?"
		args = append(args, r.IsRunning)
		args = append(args, r.Ended)
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
func (rm *roomModel) UpdateRoomParticipants(r *RoomInfo, operator string) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE "+rm.app.FormatDBTable("room_info")+
		" SET joined_participants = joined_participants "+operator+" 1 WHERE sid = ?")
	if err != nil {
		return 0, err
	}

	res, err := stmt.ExecContext(ctx, r.Sid)
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
func (rm *roomModel) UpdateNumParticipants(roomSid string, num int64) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, "UPDATE "+rm.app.FormatDBTable("room_info")+
		" SET joined_participants = ? WHERE sid = ?")
	if err != nil {
		return 0, err
	}

	res, err := stmt.ExecContext(ctx, num, roomSid)
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

func (rm *roomModel) GetRoomInfo(roomId string, sid string, isRunning int) (*RoomInfo, string) {
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
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE sid = ? AND is_running = 1", sid)

	case len(roomId) > 0 && len(sid) > 0 && isRunning == 1:
		// for sid + roomId + isRunning
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE roomId = ? AND sid = ? AND is_running = 1", roomId, sid)

	default:
		// for only sid
		query = db.QueryRowContext(ctx, "SELECT id, room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time FROM "+rm.app.FormatDBTable("room_info")+" WHERE sid = ?", sid)
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

type ActiveRoomInfo struct {
	RoomTitle          string `json:"room_title"`
	RoomId             string `json:"room_id"`
	Sid                string `json:"sid"`
	JoinedParticipants int64  `json:"joined_participants"`
	IsRunning          int    `json:"is_running"`
	IsRecording        int    `json:"is_recording"`
	IsActiveRTMP       int    `json:"is_active_rtmp"`
	WebhookUrl         string `json:"webhook_url"`
	IsBreakoutRoom     int64  `json:"is_breakout_room"`
	ParentRoomId       string `json:"parent_room_id"`
	CreationTime       int64  `json:"creation_time"`
	Metadata           string `json:"metadata"`
}

func (rm *roomModel) GetActiveRoomsInfo() ([]ActiveRoomInfo, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(rm.ctx, 3*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "select room_title, roomId, sid, joined_participants, is_running, is_recording, is_active_rtmp, webhook_url, is_breakout_room, parent_room_id, creation_time from "+rm.app.FormatDBTable("room_info")+" where is_running = ?", 1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var room ActiveRoomInfo
	var rooms []ActiveRoomInfo

	for rows.Next() {
		err = rows.Scan(&room.RoomTitle, &room.RoomId, &room.Sid, &room.JoinedParticipants, &room.IsRunning, &room.IsRecording, &room.IsActiveRTMP, &room.WebhookUrl, &room.IsBreakoutRoom, &room.ParentRoomId, &room.CreationTime)
		if err != nil {
			fmt.Println(err)
		}
		rooms = append(rooms, room)
	}

	return rooms, nil
}
