package config

import (
	"context"
	"fmt"
	"time"

	"github.com/cavaliergopher/grab/v3"
)

// GrabDownloader implements hooks.FileDownloader using the grab library.
type GrabDownloader struct {
	client *grab.Client
}

func NewGrabDownloader() *GrabDownloader {
	return &GrabDownloader{client: grab.NewClient()}
}

// Download fetches fileURL into dstDir and returns the full path of the saved file.
func (d *GrabDownloader) Download(ctx context.Context, fileURL, dstDir string, timeout time.Duration) (string, error) {
	if timeout == 0 {
		timeout = 3 * time.Minute
	}

	req, err := grab.NewRequest(dstDir, fileURL)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resp := d.client.Do(req.WithContext(dctx))
	<-resp.Done

	if err := resp.Err(); err != nil {
		return "", fmt.Errorf("failed to download file: %w", err)
	}

	return resp.Filename, nil
}
