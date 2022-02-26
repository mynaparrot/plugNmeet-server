package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
	"os"
	"strings"
	"time"
)

type authRecording struct {
	app *config.AppConfig
	db  *sql.DB
	ctx context.Context
}

func NewRecordingAuth() *authRecording {
	return &authRecording{
		app: config.AppCnf,
		db:  config.AppCnf.DB,
		ctx: context.Background(),
	}
}

type FetchRecordingsReq struct {
	RoomIds []string `json:"room_ids"`
	From    int      `json:"from"`
	Limit   int      `json:"limit"`
	OrderBy string   `json:"order_by"`
}

type FetchRecordingsResp struct {
	TotalRecordings int64           `json:"total_recordings"`
	From            int             `json:"from"`
	Limit           int             `json:"limit"`
	OrderBy         string          `json:"order_by"`
	RecordingsList  []RecordingInfo `json:"recordings_list"`
}

type RecordingInfo struct {
	RecordId         string  `json:"record_id"`
	RoomId           string  `json:"room_id"`
	RoomSid          string  `json:"room_sid"`
	FilePath         string  `json:"file_path"`
	FileSize         float64 `json:"file_size"`
	CreationTime     int64   `json:"creation_time"`
	RoomCreationTime int64   `json:"room_creation_time"`
}

func (a *authRecording) FetchRecordings(r *FetchRecordingsReq) (*FetchRecordingsResp, error) {
	db := a.db
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()

	limit := r.Limit
	orderBy := "DESC"

	if limit == 0 {
		limit = 20
	}
	if r.OrderBy == "ASC" {
		orderBy = "ASC"
	}

	var rows *sql.Rows
	var err error

	switch {
	case len(r.RoomIds) > 0:
		var args []interface{}
		for _, rd := range r.RoomIds {
			args = append(args, rd)
		}
		args = append(args, r.From)
		args = append(args, limit)

		query := "SELECT record_id, room_id, room_sid, file_path, size, creation_time, room_creation_time FROM " + a.app.FormatDBTable("recordings") + " WHERE room_id IN (?" + strings.Repeat(",?", len(r.RoomIds)-1) + ") ORDER BY id " + orderBy + " LIMIT ?,?"

		rows, err = db.QueryContext(ctx, query, args...)
	default:
		rows, err = db.QueryContext(ctx, "SELECT record_id, room_id, room_sid, file_path, size, creation_time, room_creation_time FROM "+a.app.FormatDBTable("recordings")+" ORDER BY id "+orderBy+" LIMIT ?,?", r.From, limit)
	}

	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var recording RecordingInfo
	var recordings []RecordingInfo

	for rows.Next() {
		err = rows.Scan(&recording.RecordId, &recording.RoomId, &recording.RoomSid, &recording.FilePath, &recording.FileSize, &recording.CreationTime, &recording.RoomCreationTime)
		if err != nil {
			fmt.Println(err)
		}
		recordings = append(recordings, recording)
	}

	// get total number of recordings
	var row *sql.Row
	switch {
	case len(r.RoomIds) > 0:
		var args []interface{}
		for _, rd := range r.RoomIds {
			args = append(args, rd)
		}
		query := "SELECT COUNT(*) AS total FROM " + a.app.FormatDBTable("recordings") + " WHERE room_id IN (?" + strings.Repeat(",?", len(r.RoomIds)-1) + ")"
		row = db.QueryRowContext(ctx, query, args...)
	default:
		row = db.QueryRowContext(ctx, "SELECT COUNT(*) AS total FROM "+a.app.FormatDBTable("recordings"))
	}

	var total int64
	_ = row.Scan(&total)

	result := &FetchRecordingsResp{
		TotalRecordings: total,
		From:            r.From,
		Limit:           limit,
		OrderBy:         orderBy,
		RecordingsList:  recordings,
	}

	return result, nil
}

// FetchRecording to get single recording information from DB
func (a *authRecording) FetchRecording(recordId string) (*RecordingInfo, error) {
	db := a.db
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()

	row := db.QueryRowContext(ctx, "SELECT record_id, room_id, room_sid, file_path, size, creation_time, room_creation_time FROM "+a.app.FormatDBTable("recordings")+" WHERE record_id = ?", recordId)

	recording := new(RecordingInfo)
	err := row.Scan(&recording.RecordId, &recording.RoomId, &recording.RoomSid, &recording.FilePath, &recording.FileSize, &recording.CreationTime, &recording.RoomCreationTime)

	switch {
	case err == sql.ErrNoRows:
		err = errors.New("no info found")
	case err != nil:
		err = errors.New(fmt.Sprintf("query error: %s", err.Error()))
	}

	if err != nil {
		return nil, err
	}

	return recording, nil
}

type DeleteRecordingReq struct {
	RecordId string `json:"record_id" validate:"required"`
}

func (a *authRecording) DeleteRecording(r *DeleteRecordingReq) error {
	recording, err := a.FetchRecording(r.RecordId)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/%s", config.AppCnf.RecorderInfo.RecordingFilesPath, recording.FilePath)
	err = os.Remove(path)

	if err != nil {
		// if file not exist then we can delete it from record without showing any error
		if !os.IsNotExist(err) {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[3])
		}
	}

	// no error, so we'll delete record from DB
	db := a.db
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("DELETE FROM " + a.app.FormatDBTable("recordings") + " WHERE record_id = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(r.RecordId)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	err = stmt.Close()
	if err != nil {
		return err
	}

	return nil
}

type GetDownloadTokenReq struct {
	RecordId string `json:"record_id" validate:"required"`
}

// GetDownloadToken will use same JWT token generator as Livekit is using
func (a *authRecording) GetDownloadToken(r *GetDownloadTokenReq) (string, error) {
	recording, err := a.FetchRecording(r.RecordId)
	if err != nil {
		return "", err
	}

	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(a.app.Client.Secret)}, (&jose.SignerOptions{}).WithType("JWT"))

	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    a.app.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now()),
		Expiry:    jwt.NewNumericDate(time.Now().Add(a.app.RecorderInfo.TokenValidity)),
		// format: sub_path/roomSid/filename
		Subject: recording.FilePath,
	}

	return jwt.Signed(sig).Claims(cl).CompactSerialize()
}
