package models

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *FileModel) UploadBase64EncodedData(req *plugnmeet.UploadBase64EncodedDataReq) (*plugnmeet.UploadBase64EncodedDataRes, error) {
	roomInfo, err := m.ds.GetRoomInfoByRoomId(req.GetRoomId(), 1)
	if err != nil {
		return nil, err
	}
	if roomInfo == nil {
		return nil, errors.New("room is not active")
	}

	data, err := base64.StdEncoding.DecodeString(req.GetData())
	if err != nil {
		return nil, err
	}

	saveDir := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomInfo.Sid)
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return nil, err
	}

	filePath := fmt.Sprintf("%s/%s", saveDir, req.GetFileName())
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return nil, err
	}

	mimeType, err := mimetype.DetectFile(filePath)
	if err != nil {
		_ = os.Remove(filePath)
		return nil, err
	}

	if err := m.ValidateMimeType(mimeType); err != nil {
		_ = os.Remove(filePath)
		return nil, err
	}

	return &plugnmeet.UploadBase64EncodedDataRes{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileMimeType:  mimeType.String(),
		FilePath:      fmt.Sprintf("%s/%s", roomInfo.Sid, req.GetFileName()),
		FileName:      req.GetFileName(),
		FileExtension: strings.TrimPrefix(mimeType.Extension(), "."),
	}, nil
}
