package models

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cavaliergopher/grab/v3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// DownloadAndProcessPreUploadWBfile downloads and processes a pre-uploaded whiteboard file.
// It validates the file, saves it, and triggers conversion and broadcasting.
// This should be run in a separate goroutine due to its potentially long execution time.
func (m *FileModel) DownloadAndProcessPreUploadWBfile(roomId, roomSid, fileUrl string, log *logrus.Entry) error {
	log.WithFields(logrus.Fields{
		"sub-method": "DownloadAndProcessPreUploadWBfile",
	})
	if err := m.validateRemoteFile(fileUrl); err != nil {
		log.WithError(err).Errorln("file validation failed")
		return err
	}

	downloadDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)
	if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
		log.WithError(err).Errorln("failed to create download directory")
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	// Create a new grab client
	client := grab.NewClient()
	req, err := grab.NewRequest(downloadDir, fileUrl)
	if err != nil {
		log.WithError(err).Errorln("failed to create download request")
		return fmt.Errorf("failed to create download request: %w", err)
	}

	// Create a context with a 3-minute timeout for the download.
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
	defer cancel()

	// Run the download
	resp := client.Do(req.WithContext(ctx))
	<-resp.Done // Wait for the download to complete or be canceled.

	defer os.Remove(resp.Filename)

	// Validate downloaded file type
	mType, err := mimetype.DetectFile(resp.Filename)
	if err != nil {
		log.WithError(err).Errorln("failed to detect file type")
		return fmt.Errorf("failed to detect file type: %w", err)
	}
	if err := m.ValidateMimeType(mType); err != nil {
		log.WithError(err).Errorln("downloaded file mime type is not allowed")
		return err
	}

	// Construct relative file path
	filePath := filepath.Join(roomSid, filepath.Base(resp.Filename))

	// Convert and broadcast. This is a synchronous, long-running task.
	if _, err := m.ConvertAndBroadcastWhiteboardFile(roomId, roomSid, filePath); err != nil {
		log.WithError(err).Errorln("conversion/broadcast failed")
		return fmt.Errorf("conversion/broadcast failed: %w", err)
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
