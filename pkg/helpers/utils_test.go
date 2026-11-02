package helpers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func TestIsPathInsideBase(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want bool
	}{
		{"file inside base", "/data/upload", "/data/upload/room/file.txt", true},
		{"nested dir inside base", "/data/upload", "/data/upload/room/sub", true},
		{"base itself", "/data/upload", "/data/upload", false},
		{"sibling prefix dir", "/data/upload", "/data/upload-evil/file.txt", false},
		{"unrelated dir", "/data/upload", "/etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPathInsideBase(tt.base, tt.path); got != tt.want {
				t.Errorf("isPathInsideBase(%q, %q) = %v, want %v", tt.base, tt.path, got, tt.want)
			}
		})
	}
}

func TestValidateAndGetAbsFilePath(t *testing.T) {
	root := t.TempDir()
	basePath := filepath.Join(root, "upload")
	siblingPath := filepath.Join(root, "upload-other")

	for _, dir := range []string{filepath.Join(basePath, "room1"), siblingPath} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("failed to create %s: %v", dir, err)
		}
	}
	for _, file := range []string{filepath.Join(basePath, "room1", "file.txt"), filepath.Join(siblingPath, "file.txt")} {
		if err := os.WriteFile(file, []byte("hello"), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", file, err)
		}
	}

	t.Run("valid path inside base", func(t *testing.T) {
		absPath, mType, err := ValidateAndGetAbsFilePath(basePath, "room1/file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if absPath != filepath.Join(basePath, "room1", "file.txt") {
			t.Errorf("absPath = %q, want %q", absPath, filepath.Join(basePath, "room1", "file.txt"))
		}
		if mType == nil || !mType.Is("text/plain") {
			t.Errorf("mType = %v, want text/plain", mType)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, _, err := ValidateAndGetAbsFilePath(basePath, "room1/missing.txt"); !errors.Is(err, config.ErrFileNotFound) {
			t.Errorf("err = %v, want config.ErrFileNotFound", err)
		}
	})

	invalidPaths := map[string]string{
		"parent traversal":   "../../../../etc/passwd",
		"traversal via room": "room1/../../../../etc/passwd",
		"absolute path":      "/etc/passwd",
		"escape to sibling":  "../upload-other/file.txt",
		"windows separators": `..\..\etc\passwd`,
		"base dir itself":    ".",
	}

	for name, relativePath := range invalidPaths {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ValidateAndGetAbsFilePath(basePath, relativePath); err == nil {
				t.Fatalf("expected error for %q, got nil", relativePath)
			}
		})
	}
}

func TestMakeSafeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		fallback bool
		want     string
		wantUUID bool
	}{
		{"normal filename", "report.pdf", true, "report.pdf", false},
		{"spaces and dots inside name", "my report v1.2.pdf", false, "my report v1.2.pdf", false},
		{"path traversal", "../../etc/passwd", false, "passwd", false},
		{"leading dash", "-flag.pdf", false, "flag.pdf", false},
		{"leading double dash", "--option=val.txt", false, "option=val.txt", false},
		{"leading junk", " - --evil.doc", false, "evil.doc", false},
		{"dash only with fallback", "---", true, "", true},
		{"dash only without fallback", "---", false, "", false},
		{"illegal characters", `a<b>c:d"e|f?g*.txt`, false, "a_b_c_d_e_f_g_.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MakeSafeFilename(tt.input, tt.fallback)
			if tt.wantUUID {
				if _, err := uuid.Parse(got); err != nil {
					t.Fatalf("MakeSafeFilename(%q, %v) = %q, want UUID", tt.input, tt.fallback, got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("MakeSafeFilename(%q, %v) = %q, want %q", tt.input, tt.fallback, got, tt.want)
			}
		})
	}
}
