package models

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"os"
	"strings"
)

func (m *FileModel) UploadBase64EncodedData(req *plugnmeet.UploadBase64EncodedDataReq) (*plugnmeet.UploadBase64EncodedDataRes, error) {
	roomInfo, err := m.ds.GetRoomInfoByRoomId(req.GetRoomId(), 1)
	if err != nil {
		return nil, err
	}
	if roomInfo == nil {
		return nil, errors.New("room is not active")
	}

	dec, err := base64.StdEncoding.DecodeString(req.GetData())
	if err != nil {
		return nil, err
	}

	saveDir := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomInfo.Sid)
	err = os.MkdirAll(saveDir, 0755)
	if err != nil {
		return nil, err
	}

	filePath := fmt.Sprintf("%s/%s", saveDir, req.GetFileName())
	err = os.WriteFile(filePath, dec, 0644)
	if err != nil {
		return nil, err
	}

	// we'll detect mime type again for sending data
	mType, err := mimetype.DetectFile(filePath)
	if err != nil {
		return nil, err
	}

	isAllowed := false
	detectedExtension := strings.TrimPrefix(mType.Extension(), ".")
	for _, f := range m.app.UploadFileSettings.AllowedTypes {
		if f == detectedExtension {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		_ = os.Remove(filePath)
		return nil, errors.New("file type is not allowed")
	}

	return &plugnmeet.UploadBase64EncodedDataRes{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileMimeType:  mType.String(),
		FilePath:      fmt.Sprintf("%s/%s", roomInfo.Sid, req.GetFileName()),
		FileName:      req.GetFileName(),
		FileExtension: detectedExtension,
	}, nil
}
