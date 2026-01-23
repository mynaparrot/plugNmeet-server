package models

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"sort"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

type FileModel struct {
	ctx         context.Context
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	natsService *natsservice.NatsService
	logger      *logrus.Entry
}

func NewFileModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService, logger *logrus.Logger) *FileModel {
	return &FileModel{
		ctx:         ctx,
		app:         app,
		ds:          ds,
		natsService: natsService,
		logger:      logger.WithField("model", "file"),
	}
}

func (m *FileModel) detectMimeTypeForValidation(file multipart.File) error {
	defer file.Close()

	mtype, err := mimetype.DetectReader(file)
	if err != nil {
		return err
	}

	return m.ValidateMimeType(mtype)
}

func (m *FileModel) ValidateMimeType(mtype *mimetype.MIME) error {
	allowedTypes := m.app.UploadFileSettings.AllowedTypes
	sort.Strings(allowedTypes)

	ext := strings.TrimPrefix(mtype.Extension(), ".")
	if ext == "" {
		return fmt.Errorf("invalid file")
	}

	for _, t := range allowedTypes {
		if ext == t {
			return nil
		}
	}

	return fmt.Errorf(mtype.Extension() + " file type not allowed")
}

func (m *FileModel) updateRoomMetadataWithOfficeFile(roomId string, f *ConvertWhiteboardFileRes) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return err
	}
	if roomMeta == nil {
		return errors.New("invalid nil room metadata information")
	}

	wbf := roomMeta.RoomFeatures.WhiteboardFeatures
	wbf.WhiteboardFileId = f.FileId
	wbf.FileName = f.FileName
	wbf.FilePath = f.FilePath
	wbf.TotalPages = uint32(f.TotalPages)

	err = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, roomMeta)
	if err != nil {
		m.logger.WithError(err).Errorln("metadata update failed")
	}

	return err
}

// GetRoomFilesByType retrieves all file metadata for a given room, filtered by file type.
func (m *FileModel) GetRoomFilesByType(roomId string, fileType plugnmeet.RoomUploadedFileType) (*plugnmeet.GetRoomUploadedFilesRes, error) {
	allFiles, err := m.natsService.GetAllRoomFiles(roomId)
	if err != nil {
		return nil, err
	}

	if allFiles == nil {
		// Return an empty slice instead of nil for better client-side handling
		return nil, fmt.Errorf("no files found for room")
	}

	filteredFiles := make([]*plugnmeet.RoomUploadedFileMetadata, 0, len(allFiles))
	for _, meta := range allFiles {
		if meta.FileType == fileType {
			filteredFiles = append(filteredFiles, meta)
		}
	}

	return &plugnmeet.GetRoomUploadedFilesRes{
		Status: true,
		Msg:    "success",
		Files:  filteredFiles,
	}, nil
}

func (m *FileModel) DeleteRoomUploadedDir(roomSid string) error {
	if roomSid == "" {
		return fmt.Errorf("empty sid")
	}
	path := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomSid)
	err := os.RemoveAll(path)
	if err != nil {
		m.logger.WithField("path", path).WithError(err).Errorln("can't delete room uploaded dir")
	}
	return err
}
