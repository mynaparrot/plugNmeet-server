package models

import (
	"errors"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func TestSanitizeRelativeFilePath(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     string
		wantErr  bool
	}{
		{
			name:     "room sid prefix",
			filePath: "room-sid-1/file.txt",
			want:     "room-sid-1/file.txt",
		},
		{
			name:     "third party prefix with room sid",
			filePath: "custom-prefix/room-sid-1/file.txt",
			want:     "custom-prefix/room-sid-1/file.txt",
		},
		{
			name:     "third party prefix without room sid",
			filePath: "custom-prefix/file.txt",
			want:     "custom-prefix/file.txt",
		},
		{
			name:     "path is normalized",
			filePath: "room-sid-1/./sub//file.txt",
			want:     "room-sid-1/sub/file.txt",
		},
		{
			name:     "parent traversal",
			filePath: "../../../../etc/passwd",
			wantErr:  true,
		},
		{
			name:     "traversal after prefix",
			filePath: "room-sid-1/../../etc/passwd",
			wantErr:  true,
		},
		{
			name:     "absolute path",
			filePath: "/etc/passwd",
			wantErr:  true,
		},
		{
			name:     "windows separator",
			filePath: `room-sid-1\..\..\etc\passwd`,
			wantErr:  true,
		},
		{
			name:     "temp upload dir",
			filePath: "room-sid-1/" + config.UploadFileTempDir + "/part1",
			wantErr:  true,
		},
		{
			name:     "single segment",
			filePath: "file.txt",
			wantErr:  true,
		},
		{
			name:     "current dir",
			filePath: ".",
			wantErr:  true,
		},
		{
			name:     "empty path",
			filePath: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeRelativeFilePath(tt.filePath)
			if tt.wantErr {
				assertInvalidFilePath(t, tt.filePath, got, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("cleanPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateLocalRoomFilePath(t *testing.T) {
	const roomSid = "room-sid-1"

	tests := []struct {
		name     string
		roomSid  string
		filePath string
		want     string
		wantErr  bool
	}{
		{
			name:     "valid path in room dir",
			roomSid:  roomSid,
			filePath: "room-sid-1/file.txt",
			want:     "room-sid-1/file.txt",
		},
		{
			name:     "valid path is normalized",
			roomSid:  roomSid,
			filePath: "room-sid-1/./sub//file.txt",
			want:     "room-sid-1/sub/file.txt",
		},
		{
			name:     "other room dir",
			roomSid:  roomSid,
			filePath: "other-room-sid/file.txt",
			wantErr:  true,
		},
		{
			name:     "sibling prefix dir",
			roomSid:  roomSid,
			filePath: "room-sid-1-evil/file.txt",
			wantErr:  true,
		},
		{
			name:     "third party prefix dir",
			roomSid:  roomSid,
			filePath: "custom-prefix/room-sid-1/file.txt",
			wantErr:  true,
		},
		{
			name:     "traversal after room dir",
			roomSid:  roomSid,
			filePath: "room-sid-1/../../etc/passwd",
			wantErr:  true,
		},
		{
			name:     "temp upload dir",
			roomSid:  roomSid,
			filePath: "room-sid-1/" + config.UploadFileTempDir + "/part1",
			wantErr:  true,
		},
		{
			name:     "file directly in upload root",
			roomSid:  roomSid,
			filePath: "file.txt",
			wantErr:  true,
		},
		{
			name:     "empty path",
			roomSid:  roomSid,
			filePath: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateLocalRoomFilePath(tt.roomSid, tt.filePath)
			if tt.wantErr {
				assertInvalidFilePath(t, tt.filePath, got, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("cleanPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func assertInvalidFilePath(t *testing.T, filePath, got string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error for %q, got path %q", filePath, got)
	}
	if !errors.Is(err, config.ErrInvalidFilePath) {
		t.Fatalf("err = %v, want config.ErrInvalidFilePath", err)
	}
}
