package models

import (
	"errors"
	"fmt"
	"github.com/cavaliergopher/grab/v3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"net/http"
	"os"
	"strings"
	"time"
)

// DownloadAndProcessPreUploadWBfile will download and process pre upload whiteboard file
func (m *FileModel) DownloadAndProcessPreUploadWBfile(roomId, roomSid, fileUrl string) error {
	// get file info
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Head(fileUrl)
	if err != nil {
		return err
	}

	if resp.ContentLength < 1 {
		return errors.New("invalid file type")
	} else if resp.ContentLength > config.MaxPreloadedWhiteboardFileSize {
		return errors.New(fmt.Sprintf("Allowd %d but given %d", config.MaxPreloadedWhiteboardFileSize, resp.ContentLength))
	}

	cType := resp.Header.Get("Content-Type")
	if cType == "" {
		return errors.New("invalid Content-Type")
	}

	// validate file type
	mType := mimetype.Lookup(cType)
	err = m.ValidateMimeType(mType)
	if err != nil {
		return err
	}

	downloadDir := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomSid)
	if _, err = os.Stat(downloadDir); os.IsNotExist(err) {
		err = os.MkdirAll(downloadDir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	// now download the file
	gres, err := grab.Get(downloadDir, fileUrl)
	if err != nil {
		return err
	}
	// remove file at the end
	defer os.RemoveAll(gres.Filename)

	// double check to make sure that dangerous file wasn't uploaded
	mType, err = mimetype.DetectFile(gres.Filename)
	if err != nil {
		return err
	}
	err = m.ValidateMimeType(mType)
	if err != nil {
		return err
	}

	ms := strings.SplitN(gres.Filename, "/", -1)
	filePath := fmt.Sprintf("%s/%s", roomSid, ms[len(ms)-1])

	_, err = m.ConvertAndBroadcastWhiteboardFile(roomId, roomSid, filePath)
	if err != nil {
		return err
	}

	return nil
}
