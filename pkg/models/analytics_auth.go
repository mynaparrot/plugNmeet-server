package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"os"
	"strings"
	"time"
)

type AnalyticsAuthModel struct {
	app *config.AppConfig
	db  *sql.DB
	ctx context.Context
}

func NewAnalyticsAuthModel() *AnalyticsAuthModel {
	return &AnalyticsAuthModel{
		app: config.AppCnf,
		db:  config.AppCnf.DB,
		ctx: context.Background(),
	}
}

func (m *AnalyticsAuthModel) FetchAnalytics(r *plugnmeet.FetchAnalyticsReq) (*plugnmeet.FetchAnalyticsResult, error) {
	db := m.db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
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

		query := "SELECT room_id, file_id, file_name, file_size, room_creation_time, creation_time FROM " + m.app.FormatDBTable("room_analytics") + " WHERE room_id IN (?" + strings.Repeat(",?", len(r.RoomIds)-1) + ") ORDER BY id " + orderBy + " LIMIT ?,?"

		rows, err = db.QueryContext(ctx, query, args...)
	default:
		rows, err = db.QueryContext(ctx, "SELECT room_id, file_id, file_name, file_size, room_creation_time, creation_time FROM "+m.app.FormatDBTable("room_analytics")+" ORDER BY id "+orderBy+" LIMIT ?,?", r.From, limit)
	}

	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var analytics []*plugnmeet.AnalyticsInfo

	for rows.Next() {
		var analytic plugnmeet.AnalyticsInfo

		err = rows.Scan(&analytic.RoomId, &analytic.FileId, &analytic.FileName, &analytic.FileSize, &analytic.RoomCreationTime, &analytic.CreationTime)
		if err != nil {
			fmt.Println(err)
		}
		analytics = append(analytics, &analytic)
	}

	// get total number of analytics
	var row *sql.Row
	switch {
	case len(r.RoomIds) > 0:
		var args []interface{}
		for _, rd := range r.RoomIds {
			args = append(args, rd)
		}
		query := "SELECT COUNT(*) AS total FROM " + m.app.FormatDBTable("room_analytics") + " WHERE room_id IN (?" + strings.Repeat(",?", len(r.RoomIds)-1) + ")"
		row = db.QueryRowContext(ctx, query, args...)
	default:
		row = db.QueryRowContext(ctx, "SELECT COUNT(*) AS total FROM "+m.app.FormatDBTable("room_analytics"))
	}

	var total int64
	_ = row.Scan(&total)

	result := &plugnmeet.FetchAnalyticsResult{
		TotalAnalytics: total,
		From:           r.From,
		Limit:          limit,
		OrderBy:        orderBy,
		AnalyticsList:  analytics,
	}

	return result, nil
}

func (m *AnalyticsAuthModel) fetchAnalytic(fileId string) (*plugnmeet.AnalyticsInfo, error) {
	db := m.db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	row := db.QueryRowContext(ctx, "SELECT room_id, file_id, file_name, file_size, room_creation_time, creation_time FROM "+m.app.FormatDBTable("room_analytics")+" WHERE file_id = ?", fileId)

	analytic := new(plugnmeet.AnalyticsInfo)
	err := row.Scan(&analytic.RoomId, &analytic.FileId, &analytic.FileName, &analytic.FileSize, &analytic.RoomCreationTime, &analytic.CreationTime)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = errors.New("no info found")
	case err != nil:
		err = errors.New(fmt.Sprintf("query error: %s", err.Error()))
	}

	if err != nil {
		return nil, err
	}

	return analytic, nil
}

func (m *AnalyticsAuthModel) getAnalyticByRoomTableId(roomTableId int64) (*plugnmeet.AnalyticsInfo, error) {
	db := m.db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	row := db.QueryRowContext(ctx, "SELECT room_id, file_id, file_name, file_size, room_creation_time, creation_time FROM "+m.app.FormatDBTable("room_analytics")+" WHERE room_table_id = ?", roomTableId)

	analytic := new(plugnmeet.AnalyticsInfo)
	err := row.Scan(&analytic.RoomId, &analytic.FileId, &analytic.FileName, &analytic.FileSize, &analytic.RoomCreationTime, &analytic.CreationTime)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = errors.New("no info found")
	case err != nil:
		err = errors.New(fmt.Sprintf("query error: %s", err.Error()))
	}

	if err != nil {
		return nil, err
	}

	return analytic, nil
}

func (m *AnalyticsAuthModel) DeleteAnalytics(r *plugnmeet.DeleteAnalyticsReq) error {
	analytic, err := m.fetchAnalytic(r.FileId)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/%s", *config.AppCnf.AnalyticsSettings.FilesStorePath, analytic.FileName)

	// delete main file
	err = os.Remove(path)
	if err != nil {
		// if file not exist then we can delete it from record without showing any error
		if !os.IsNotExist(err) {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// delete compressed, if any
	_ = os.Remove(path + ".fiber.gz")

	// no error, so we'll delete record from DB
	db := m.db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("DELETE FROM " + m.app.FormatDBTable("room_analytics") + " WHERE file_id = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(r.FileId)
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

// GetAnalyticsDownloadToken will use the same JWT token generator as plugNmeet is using
func (m *AnalyticsAuthModel) GetAnalyticsDownloadToken(r *plugnmeet.GetAnalyticsDownloadTokenReq) (string, error) {
	analytic, err := m.fetchAnalytic(r.FileId)
	if err != nil {
		return "", err
	}

	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(m.app.Client.Secret)}, (&jose.SignerOptions{}).WithType("JWT"))

	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    m.app.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now().UTC()),
		Expiry:    jwt.NewNumericDate(time.Now().UTC().Add(*m.app.AnalyticsSettings.TokenValidity)),
		Subject:   analytic.FileName,
	}

	return jwt.Signed(sig).Claims(cl).CompactSerialize()
}

// VerifyAnalyticsToken verify token & provide file path
func (m *AnalyticsAuthModel) VerifyAnalyticsToken(token string) (string, int, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(config.AppCnf.Client.Secret), &out); err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	if err = out.Validate(jwt.Expected{Issuer: config.AppCnf.Client.ApiKey, Time: time.Now().UTC()}); err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	file := fmt.Sprintf("%s/%s", *config.AppCnf.AnalyticsSettings.FilesStorePath, out.Subject)
	_, err = os.Lstat(file)

	if err != nil {
		ms := strings.SplitN(err.Error(), "/", -1)
		return "", fiber.StatusNotFound, errors.New(ms[len(ms)-1])
	}

	return file, fiber.StatusOK, nil
}
