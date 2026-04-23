package models

import (
	"context"
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
func (m *FileModel) DownloadAndProcessPreUploadWBfile(roomId, roomSid, fileUrl string, log *logrus.Entry) (*ConvertWhiteboardFileRes, error) {
	log = log.WithFields(logrus.Fields{
		"sub-method": "DownloadAndProcessPreUploadWBfile",
	})
	if err := m.validateRemoteFile(fileUrl); err != nil {
		log.WithError(err).Errorln("file validation failed")
		return nil, err
	}

	downloadDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)
	if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
		log.WithError(err).Errorln("failed to create download directory")
		return nil, fmt.Errorf("failed to create download directory: %w", err)
	}

	// Create a new grab client
	client := grab.NewClient()
	req, err := grab.NewRequest(downloadDir, fileUrl)
	if err != nil {
		log.WithError(err).Errorln("failed to create download request")
		return nil, fmt.Errorf("failed to create download request: %w", err)
	}

	// Create a context with a 3-minute timeout for the download.
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
	defer cancel()

	// Run the download
	resp := client.Do(req.WithContext(ctx))
	<-resp.Done // Wait for the download to complete or be canceled.

	// Check for download errors (e.g., timeout, connection issues)
	if err := resp.Err(); err != nil {
		log.WithError(err).Errorln("failed to download file")
		return nil, fmt.Errorf("failed to download file: %w", err)
	}

	defer os.Remove(resp.Filename)

	// Verify actual file size as the server might not have sent Content-Length header
	stat, err := os.Stat(resp.Filename)
	if err != nil {
		log.WithError(err).Errorln("failed to stat downloaded file")
		return nil, fmt.Errorf("failed to stat downloaded file: %w", err)
	}
	if stat.Size() > config.MaxPreloadedWhiteboardFileSize {
		log.WithFields(logrus.Fields{
			"size":     stat.Size(),
			"max_size": config.MaxPreloadedWhiteboardFileSize,
		}).Errorln("downloaded file is too large")
		return nil, fmt.Errorf("downloaded file is too large: allowed %d bytes, got %d", config.MaxPreloadedWhiteboardFileSize, stat.Size())
	}

	// Validate downloaded file type
	mType, err := mimetype.DetectFile(resp.Filename)
	if err != nil {
		log.WithError(err).Errorln("failed to detect file type")
		return nil, fmt.Errorf("failed to detect file type: %w", err)
	}
	if err := m.ValidateMimeType(mType); err != nil {
		log.WithError(err).Errorln("downloaded file mime type is not allowed")
		return nil, err
	}

	// Construct relative file path
	filePath := filepath.Join(roomSid, filepath.Base(resp.Filename))

	// Convert and broadcast. This is a synchronous, long-running task.
	res, err := m.ConvertAndBroadcastWhiteboardFile(roomId, roomSid, filePath)
	if err != nil {
		log.WithError(err).Errorln("conversion/broadcast failed")
		return nil, fmt.Errorf("conversion/broadcast failed: %w", err)
	}

	return res, nil
}

// validateRemoteFile checks the file's headers for size and MIME type.
func (m *FileModel) validateRemoteFile(fileUrl string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(fileUrl)
	if err != nil {
		return fmt.Errorf("failed to fetch file headers: %w", err)
	}
	defer resp.Body.Close()

	// Only check ContentLength if it is provided (> 0)
	if resp.ContentLength > 0 && resp.ContentLength > config.MaxPreloadedWhiteboardFileSize {
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
