package models

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
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

	fileFullPath, err := m.downloadFileToDest(ctx, fileUrl, downloadDir, log)
	if err != nil {
		return "", err
	}

	// Verify actual file size as the server might not have sent Content-Length header
	stat, err := os.Stat(fileFullPath)
	if err != nil {
		log.WithError(err).Errorln("failed to stat downloaded file")
		return "", fmt.Errorf("failed to stat downloaded file: %w", err)
	}
	if stat.Size() > int64(maxSize) {
		log.WithFields(logrus.Fields{
			"size":     stat.Size(),
			"max_size": maxSize,
		}).Errorln("downloaded file is too large")
		_ = os.Remove(fileFullPath)
		return "", fmt.Errorf("downloaded file is too large: allowed %d bytes, got %d", maxSize, stat.Size())
	}

	// Validate downloaded file type
	mType, err := mimetype.DetectFile(fileFullPath)
	if err != nil {
		log.WithError(err).Errorln("failed to detect file type")
		_ = os.Remove(fileFullPath)
		return "", fmt.Errorf("failed to detect file type: %w", err)
	}
	if err := m.ValidateMimeType(mType); err != nil {
		log.WithError(err).Errorln("downloaded file mime type is not allowed")
		_ = os.Remove(fileFullPath)
		return "", err
	}

	safeFileName := helpers.MakeSafeFilename(filepath.Base(fileFullPath), true)
	newPath := filepath.Join(filepath.Dir(fileFullPath), safeFileName)

	if err := os.Rename(fileFullPath, newPath); err != nil {
		log.WithError(err).Errorln("failed to rename downloaded file")
		_ = os.Remove(fileFullPath)
		return "", fmt.Errorf("failed to rename downloaded file: %w", err)
	}

	return newPath, nil
}

// validateRemoteFile checks the file's url and headers for size and MIME type.
func (m *FileModel) validateRemoteFile(fileUrl string, maxSize uint64) error {
	if err := validateRemoteFileUrl(fileUrl); err != nil {
		return err
	}

	client := remoteFileHTTPClient(5 * time.Second)
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

func (m *FileModel) downloadFileToDest(ctx context.Context, fileUrl, dstDir string, log *logrus.Entry) (fileFullPath string, err error) {
	log = log.WithField("sub-method", "downloadFileToDest")

	client := grab.NewClient()
	// no client timeout here, the request context controls the download duration
	client.HTTPClient = remoteFileHTTPClient(0)

	req, err := grab.NewRequest(dstDir, fileUrl)
	if err != nil {
		log.WithError(err).Error("failed to create download request")
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	gctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	resp := client.Do(req.WithContext(gctx))
	<-resp.Done

	if err := resp.Err(); err != nil {
		log.WithError(err).Error("failed to download file")
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	return resp.Filename, nil
}

// validateRemoteFileUrl only accepts absolute http(s) urls with a host.
func validateRemoteFileUrl(fileUrl string) error {
	u, err := url.Parse(fileUrl)
	if err != nil {
		return fmt.Errorf("invalid file url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid file url scheme: %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("invalid file url: missing host")
	}
	return nil
}

// remoteFileHTTPClient returns a client that refuses to connect to non-public
// addresses so remote file urls cannot be used for SSRF.
func remoteFileHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("invalid remote address %q", host)
			}
			if isBlockedRemoteIP(ip) {
				return fmt.Errorf("remote address %q is not allowed", host)
			}
			return nil
		},
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:        dialer.DialContext,
			DisableCompression: true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("invalid redirect scheme: %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// isBlockedRemoteIP reports whether the address must not be dialed for remote
// file downloads.
func isBlockedRemoteIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	ip = ip.Unmap()

	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
