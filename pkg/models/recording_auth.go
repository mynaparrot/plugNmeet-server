package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
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

func (a *authRecording) FetchRecordings(r *plugnmeet.FetchRecordingsReq) (*plugnmeet.FetchRecordingsRes, error) {
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
	var recordings []*plugnmeet.RecordingInfo

	for rows.Next() {
		var recording plugnmeet.RecordingInfo
		var rSid sql.NullString

		err = rows.Scan(&recording.RecordId, &recording.RoomId, &rSid, &recording.FilePath, &recording.FileSize, &recording.CreationTime, &recording.RoomCreationTime)
		if err != nil {
			fmt.Println(err)
		}
		recording.RoomSid = rSid.String
		recordings = append(recordings, &recording)
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

	result := &plugnmeet.FetchRecordingsRes{
		TotalRecordings: total,
		From:            r.From,
		Limit:           limit,
		OrderBy:         orderBy,
		RecordingsList:  recordings,
	}

	if result.GetTotalRecordings() == 0 {
		result.TotalRecordings = 0
	}

	return result, nil
}

// FetchRecording to get single recording information from DB
func (a *authRecording) FetchRecording(recordId string) (*plugnmeet.RecordingInfo, error) {
	db := a.db
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()

	row := db.QueryRowContext(ctx, "SELECT record_id, room_id, room_sid, file_path, size, creation_time, room_creation_time FROM "+a.app.FormatDBTable("recordings")+" WHERE record_id = ?", recordId)

	recording := new(plugnmeet.RecordingInfo)
	var rSid sql.NullString

	err := row.Scan(&recording.RecordId, &recording.RoomId, &rSid, &recording.FilePath, &recording.FileSize, &recording.CreationTime, &recording.RoomCreationTime)

	recording.RoomSid = rSid.String

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

func (a *authRecording) DeleteRecording(r *plugnmeet.DeleteRecordingReq) error {
	recording, err := a.FetchRecording(r.RecordId)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/%s", config.AppCnf.RecorderInfo.RecordingFilesPath, recording.FilePath)

	// delete main file
	err = os.Remove(path)
	if err != nil {
		// if file not exist then we can delete it from record without showing any error
		if !os.IsNotExist(err) {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[3])
		}
	}

	// delete compressed, if any
	_ = os.Remove(path + ".fiber.gz")

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
func (a *authRecording) GetDownloadToken(r *plugnmeet.GetDownloadTokenReq) (string, error) {
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

// VerifyRecordingToken verify token & provide file path
func (a *authRecording) VerifyRecordingToken(token string) (string, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return "", err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(config.AppCnf.Client.Secret), &out); err != nil {
		return "", err
	}

	if err = out.Validate(jwt.Expected{Issuer: config.AppCnf.Client.ApiKey, Time: time.Now()}); err != nil {
		return "", err
	}

	file := fmt.Sprintf("%s/%s", config.AppCnf.RecorderInfo.RecordingFilesPath, out.Subject)
	_, err = os.Lstat(file)

	if err != nil {
		ms := strings.SplitN(err.Error(), "/", -1)
		return "", errors.New(ms[len(ms)-1])
	}

	return file, nil
}
