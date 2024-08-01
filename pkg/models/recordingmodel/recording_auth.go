package recordingmodel

import (
	"errors"
	"fmt"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"strings"
	"time"
)

type AuthRecording struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
}

func NewRecordingAuth(app *config.AppConfig, ds *dbservice.DatabaseService) *AuthRecording {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}

	return &AuthRecording{
		app: config.GetConfig(),
		ds:  ds,
	}
}

func (a *AuthRecording) FetchRecordings(r *plugnmeet.FetchRecordingsReq) (*plugnmeet.FetchRecordingsResult, error) {
	data, total, err := a.ds.GetRecordings(r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}
	var recordings []*plugnmeet.RecordingInfo
	for _, v := range data {
		recording := &plugnmeet.RecordingInfo{
			RecordId:         v.RecordID,
			RoomId:           v.RoomID,
			RoomSid:          v.RoomSid.String,
			FilePath:         v.FilePath,
			FileSize:         float32(v.Size),
			CreationTime:     v.CreationTime,
			RoomCreationTime: v.RoomCreationTime,
		}
		recordings = append(recordings, recording)
	}

	result := &plugnmeet.FetchRecordingsResult{
		TotalRecordings: total,
		From:            r.From,
		Limit:           r.Limit,
		OrderBy:         r.OrderBy,
		RecordingsList:  recordings,
	}

	return result, nil
}

// FetchRecording to get single recording information from DB
func (a *AuthRecording) FetchRecording(recordId string) (*plugnmeet.RecordingInfo, error) {
	v, err := a.ds.GetRecording(recordId)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, errors.New("no info found")
	}
	recording := &plugnmeet.RecordingInfo{
		RecordId:         v.RecordID,
		RoomId:           v.RoomID,
		RoomSid:          v.RoomSid.String,
		FilePath:         v.FilePath,
		FileSize:         float32(v.Size),
		CreationTime:     v.CreationTime,
		RoomCreationTime: v.RoomCreationTime,
	}

	return recording, nil
}

func (a *AuthRecording) RecordingInfo(req *plugnmeet.RecordingInfoReq) (*plugnmeet.RecordingInfoRes, error) {
	recording, err := a.FetchRecording(req.RecordId)
	if err != nil {
		return nil, err
	}

	pastRoomInfo := new(plugnmeet.PastRoomInfo)
	// SID can't be null, so we'll check before
	if recording.GetRoomSid() != "" {
		if roomInfo, err := a.ds.GetRoomInfoBySid(recording.GetRoomSid(), nil); err == nil && roomInfo != nil {
			pastRoomInfo = &plugnmeet.PastRoomInfo{
				RoomTitle:          roomInfo.RoomTitle,
				RoomId:             roomInfo.RoomId,
				RoomSid:            roomInfo.Sid,
				JoinedParticipants: roomInfo.JoinedParticipants,
				WebhookUrl:         roomInfo.WebhookUrl,
				Created:            roomInfo.Created.Format("2006-01-02 15:04:05"),
				Ended:              roomInfo.Ended.Format("2006-01-02 15:04:05"),
			}
			if an, err := a.ds.GetAnalyticByRoomTableId(roomInfo.ID); err == nil && an != nil {
				pastRoomInfo.AnalyticsFileId = an.FileID
			}
		}
	}

	return &plugnmeet.RecordingInfoRes{
		Status:        true,
		Msg:           "success",
		RecordingInfo: recording,
		RoomInfo:      pastRoomInfo,
	}, nil
}

func (a *AuthRecording) DeleteRecording(r *plugnmeet.DeleteRecordingReq) error {
	recording, err := a.FetchRecording(r.RecordId)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/%s", config.GetConfig().RecorderInfo.RecordingFilesPath, recording.FilePath)
	fileExist := true

	f, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, err.(*os.PathError)) {
			log.Errorln(recording.FilePath + " does not exist, so deleting from DB without stopping")
			fileExist = false
		} else {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// if file not exists then will delete
	// if not, we can just skip this & delete from DB
	if fileExist {
		err = os.Remove(path)
		if err != nil {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// delete compressed, if any
	_ = os.Remove(path + ".fiber.gz")
	// delete record info file too
	_ = os.Remove(path + ".json")

	// we will check if the directory is empty or not
	// if empty then better to delete that directory
	if fileExist {
		dir := strings.Replace(path, f.Name(), "", 1)
		if dir != config.GetConfig().RecorderInfo.RecordingFilesPath {
			empty, err := a.isDirEmpty(dir)
			if err == nil && empty {
				err = os.Remove(dir)
				if err != nil {
					log.Error(err)
				}
			}
		}
	}

	// no error, so we'll delete record from DB
	_, err = a.ds.DeleteRecording(r.RecordId)
	if err != nil {
		return err
	}
	return nil
}

func (a *AuthRecording) isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1) // Or f.Readdir(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err // Either not empty or error, suits both cases
}

// GetDownloadToken will use the same JWT token generator as plugNmeet is using
func (a *AuthRecording) GetDownloadToken(r *plugnmeet.GetDownloadTokenReq) (string, error) {
	recording, err := a.FetchRecording(r.RecordId)
	if err != nil {
		return "", err
	}

	return a.CreateTokenForDownload(recording.FilePath)
}

// CreateTokenForDownload will generate token
// path format: sub_path/roomSid/filename
func (a *AuthRecording) CreateTokenForDownload(path string) (string, error) {
	return auth.GenerateTokenForDownloadRecording(path, a.app.Client.ApiKey, a.app.Client.Secret, a.app.RecorderInfo.TokenValidity)
}

// VerifyRecordingToken verify token & provide file path
func (a *AuthRecording) VerifyRecordingToken(token string) (string, int, error) {
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(config.GetConfig().Client.Secret), &out); err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	if err = out.Validate(jwt.Expected{Issuer: config.GetConfig().Client.ApiKey, Time: time.Now().UTC()}); err != nil {
		return "", fiber.StatusUnauthorized, err
	}

	file := fmt.Sprintf("%s/%s", config.GetConfig().RecorderInfo.RecordingFilesPath, out.Subject)
	_, err = os.Lstat(file)

	if err != nil {
		ms := strings.SplitN(err.Error(), "/", -1)
		return "", fiber.StatusNotFound, errors.New(ms[len(ms)-1])
	}

	return file, fiber.StatusOK, nil
}
