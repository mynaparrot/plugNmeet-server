package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func TestValidateExternalUrl(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "https", url: "https://example.com/video.mp4"},
		{name: "http with port", url: "http://example.com:8080/video.mp4"},
		{name: "upper case scheme", url: "HTTPS://example.com/video.mp4"},
		{name: "missing scheme", url: "example.com/video.mp4", wantErr: true},
		{name: "empty host", url: "https:///video.mp4", wantErr: true},
		{name: "javascript", url: "javascript:alert(1)", wantErr: true},
		{name: "data", url: "data:text/html,<script>alert(1)</script>", wantErr: true},
		{name: "file", url: "file:///etc/passwd", wantErr: true},
		{name: "ftp", url: "ftp://example.com/video.mp4", wantErr: true},
		{name: "empty", url: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExternalUrl(tt.url)
			if tt.wantErr != (err != nil) {
				t.Fatalf("validateExternalUrl(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestExternalMediaHandlersRejectUnsafeUrls(t *testing.T) {
	m := &RoomModel{logger: logrus.NewEntry(logrus.New())}
	unsafe := []string{"javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "file:///etc/passwd"}

	for _, url := range unsafe {
		linkReq := &plugnmeet.ExternalDisplayLinkReq{
			RoomId: "room-id",
			UserId: "user-id",
			Url:    &url,
		}
		if _, err := m.startExternalDisplay(linkReq); err == nil {
			t.Errorf("startExternalDisplay accepted unsafe url %q", url)
		}

		mediaReq := &plugnmeet.ExternalMediaPlayerReq{
			RoomId: "room-id",
			UserId: "user-id",
			Url:    &url,
		}
		if _, err := m.startExternalMediaPlayBack(mediaReq); err == nil {
			t.Errorf("startExternalMediaPlayBack accepted unsafe url %q", url)
		}
	}
}
