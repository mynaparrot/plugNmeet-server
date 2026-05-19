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
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// DownloadAndProcessWhiteboardFile downloads and processes a pre-uploaded whiteboard file.
// It validates the file, saves it, and triggers conversion and broadcasting.
// This should be run in a separate goroutine due to its potentially long execution time.
func (m *FileModel) DownloadAndProcessWhiteboardFile(roomId, roomSid, fileUrl string, maxSize uint64, lock *redisservice.Lock, log *logrus.Entry) (*ConvertWhiteboardFileRes, error) {
	log = log.WithFields(logrus.Fields{
		"sub-method": "DownloadAndProcessWhiteboardFile",
	})

	filePath, err := m.downloadFile(m.ctx, fileUrl, roomSid, maxSize, log)
	if err != nil {
		if lock != nil {
			_ = lock.Unlock(context.Background())
		}
		return nil, err
	}
	log.Info("File downloaded successfully")

	// Construct relative file path
	relativeFilePath := filepath.Join(roomSid, filepath.Base(filePath))

	// Convert and broadcast. This is a synchronous, long-running task.
	res, err := m.ConvertAndBroadcastWhiteboardFile(m.ctx, roomId, roomSid, relativeFilePath, nil, lock, log)
	if err != nil {
		log.WithError(err).Errorln("conversion/broadcast failed")
		return nil, fmt.Errorf("conversion/broadcast failed: %w", err)
	}
	log.Info("File converted and broadcasted successfully")

	return res, nil
}

// downloadFile will download a file from url, validate and return store path
func (m *FileModel) downloadFile(ctx context.Context, fileUrl, roomSid string, maxSize uint64, log *logrus.Entry) (string, error) {
	log = log.WithField("sub-method", "downloadFile")
	if err := m.validateRemoteFile(fileUrl, maxSize); err != nil {
		log.WithError(err).Errorln("file validation failed")
		return "", err
	}

	downloadDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)
	if err := os.MkdirAll(downloadDir, os.ModePerm); err != nil {
		log.WithError(err).Errorln("failed to create download directory")
		return "", fmt.Errorf("failed to create download directory: %w", err)
	}

	// Create a new grab client
	client := grab.NewClient()
	req, err := grab.NewRequest(downloadDir, fileUrl)
	if err != nil {
		log.WithError(err).Errorln("failed to create download request")
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	// Create a context with a 3-minute timeout for the download.
	gctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// Run the download
	resp := client.Do(req.WithContext(gctx))
	<-resp.Done // Wait for the download to complete or be canceled.

	// Check for download errors (e.g., timeout, connection issues)
	if err := resp.Err(); err != nil {
		log.WithError(err).Errorln("failed to download file")
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	// Verify actual file size as the server might not have sent Content-Length header
	stat, err := os.Stat(resp.Filename)
	if err != nil {
		log.WithError(err).Errorln("failed to stat downloaded file")
		return "", fmt.Errorf("failed to stat downloaded file: %w", err)
	}
	if stat.Size() > int64(maxSize) {
		log.WithFields(logrus.Fields{
			"size":     stat.Size(),
			"max_size": maxSize,
		}).Errorln("downloaded file is too large")
		return "", fmt.Errorf("downloaded file is too large: allowed %d bytes, got %d", maxSize, stat.Size())
	}

	// Validate downloaded file type
	mType, err := mimetype.DetectFile(resp.Filename)
	if err != nil {
		log.WithError(err).Errorln("failed to detect file type")
		return "", fmt.Errorf("failed to detect file type: %w", err)
	}
	if err := m.ValidateMimeType(mType); err != nil {
		log.WithError(err).Errorln("downloaded file mime type is not allowed")
		return "", err
	}

	safeFileName := helpers.MakeSafeFilename(filepath.Base(resp.Filename))
	newPath := filepath.Join(filepath.Dir(resp.Filename), safeFileName)

	if err := os.Rename(resp.Filename, newPath); err != nil {
		log.WithError(err).Errorln("failed to rename downloaded file")
		_ = os.Remove(resp.Filename)
		return "", fmt.Errorf("failed to rename downloaded file: %w", err)
	}

	return newPath, nil
}

// validateRemoteFile checks the file's headers for size and MIME type.
func (m *FileModel) validateRemoteFile(fileUrl string, maxSize uint64) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Head(fileUrl)
	if err != nil {
		return fmt.Errorf("failed to fetch file headers: %w", err)
	}
	defer resp.Body.Close()

	// Only check ContentLength if it is provided (> 0)
	if resp.ContentLength > 0 && resp.ContentLength > int64(maxSize) {
		return fmt.Errorf("file too large: allowed %d bytes, got %d", maxSize, resp.ContentLength)
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
