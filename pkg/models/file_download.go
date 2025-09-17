package models

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cavaliergopher/grab/v3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// DownloadAndProcessPreUploadWBfile downloads and processes a pre-uploaded whiteboard file.
// It validates the file, saves it, and triggers conversion and broadcasting.
// This should be run in a separate goroutine due to its potentially long execution time.
func (m *FileModel) DownloadAndProcessPreUploadWBfile(roomId, roomSid, fileUrl string) error {
	if err := m.validateRemoteFile(fileUrl); err != nil {
		m.logger.WithError(err).Error("file validation failed")
		return errors.New("file validation failed")
	}

	downloadDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)
	if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
		m.logger.WithError(err).Errorln("failed to create download directory")
		return errors.New("failed to create download directory")
	}

	// Download the file
	resp, err := grab.Get(downloadDir, fileUrl)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to download file")
		return errors.New("failed to download file")
	}
	defer os.RemoveAll(resp.Filename)

	// Validate downloaded file type
	mType, err := mimetype.DetectFile(resp.Filename)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to detect file type")
		return errors.New("failed to detect file type")
	}
	if err := m.ValidateMimeType(mType); err != nil {
		return err
	}

	// Construct relative file path
	filePath := filepath.Join(roomSid, filepath.Base(resp.Filename))

	// Convert and broadcast
	if _, err := m.ConvertAndBroadcastWhiteboardFile(roomId, roomSid, filePath); err != nil {
		m.logger.WithError(err).Errorln("conversion/broadcast failed")
		return errors.New("conversion/broadcast failed")
	}

	return nil
}

// validateRemoteFile checks the file's headers for size and MIME type.
func (m *FileModel) validateRemoteFile(fileUrl string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(fileUrl)
	if err != nil {
		return fmt.Errorf("failed to fetch file headers: %w", err)
	}
	defer resp.Body.Close()

	if resp.ContentLength < 1 {
		return errors.New("invalid file: empty content")
	}
	if resp.ContentLength > config.MaxPreloadedWhiteboardFileSize {
		return fmt.Errorf("file too large: allowed %d bytes, got %d", config.MaxPreloadedWhiteboardFileSize, resp.ContentLength)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		return fmt.Errorf("missing Content-Type header")
	}

	mType := mimetype.Lookup(contentType)
	if err := m.ValidateMimeType(mType); err != nil {
		return fmt.Errorf("invalid MIME type: %w", err)
	}

	return nil
}
