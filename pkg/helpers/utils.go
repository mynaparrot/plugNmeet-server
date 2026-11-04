package helpers

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func ToFixed(num float32, precision int) float32 {
	pow := math.Pow(10, float64(precision))
	var rounded float64
	if num < 0 {
		rounded = float64(num)*pow - 0.5
	} else {
		rounded = float64(num)*pow + 0.5
	}
	truncated := math.Trunc(rounded)
	result := truncated / pow
	return float32(result)
}

func GenerateSipPin(length int) string {
	var builder strings.Builder
	builder.Grow(length)
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return builder.String()
		}
		builder.WriteByte(byte('0' + num.Int64()))
	}
	return builder.String()
}

// SanitizeRelativeFilePath normalizes a client supplied path and blocks anything that
// could escape the storage root.
func SanitizeRelativeFilePath(filePath string) (string, error) {
	if filePath == "" {
		return "", config.ErrInvalidFilePath
	}
	if strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "\\") || strings.Contains(filePath, "..") {
		return "", config.ErrInvalidFilePath
	}

	cleanPath := path.Clean(filepath.ToSlash(filePath))
	if cleanPath == "." || cleanPath == "/" || strings.HasPrefix(cleanPath, "/") {
		return "", config.ErrInvalidFilePath
	}

	segments := strings.Split(cleanPath, "/")
	if len(segments) < 2 {
		return "", config.ErrInvalidFilePath
	}

	for _, segment := range segments {
		if segment == ".." || segment == "." || segment == config.UploadFileTempDir {
			return "", config.ErrInvalidFilePath
		}
	}

	return cleanPath, nil
}

// A minimal regex to remove characters that are illegal in Windows filenames,
// plus the null byte which is a general security risk.
// We explicitly include / and \ even though filepath.Base handles them, for clarity.
var minimalIllegalChars = regexp.MustCompile(`[<>:"/\\|?*\x00]`)

// MakeSafeFilename sanitizes a string to be used as a safe filename.
// It is designed to be as permissive as possible, only removing characters
// that are illegal on major filesystems or pose a security risk.
func MakeSafeFilename(name string, fallbackToUUID bool) string {
	// 1. Prevent path traversal. This is the most important security step.
	name = filepath.Base(filepath.Clean(name))

	// 2. Replace the minimal set of illegal/unsafe characters with an underscore.
	safeName := minimalIllegalChars.ReplaceAllString(name, "_")

	// 3. Trim leading/trailing dots and spaces, which can cause issues on Windows.
	// Leading dashes are also trimmed so the result is never read as a CLI flag.
	safeName = strings.TrimLeft(safeName, " .-")
	safeName = strings.TrimRight(safeName, " .")

	// 4. Provide a fallback if the sanitization results in an empty string.
	if safeName == "" && fallbackToUUID {
		return uuid.New().String()
	}

	// 5. Enforce a standard max length to prevent filesystem errors.
	const maxBytes = 255
	if len(safeName) > maxBytes {
		runes := []rune(safeName)
		// Iteratively drop the last rune until the byte length is within the limit
		for len(string(runes)) > maxBytes {
			runes = runes[:len(runes)-1]
		}
		safeName = string(runes)
	}

	return safeName
}

// isPathInsideBase reports whether absPath is strictly inside absBasePath.
// The separator suffix prevents sibling directories such as /upload-evil
// from being accepted for the base /upload.
func isPathInsideBase(absBasePath, absPath string) bool {
	return strings.HasPrefix(absPath, absBasePath+string(filepath.Separator))
}

// ValidateAndGetAbsFilePath performs security checks on a file path and detects its MIME type.
// It returns the sanitized absolute path, the detected MIME type, or an error if any check fails.
func ValidateAndGetAbsFilePath(basePath, relativePath string) (string, *mimetype.MIME, error) {
	// Check for path traversal sequences in the raw relative path.
	if strings.Contains(relativePath, "..") {
		return "", nil, errors.New("invalid file path: contains '..'")
	}

	// Join the base path with the relative path.
	joinedPath := filepath.Join(basePath, relativePath)

	// Get the absolute path of the base directory. This is our security boundary.
	absBasePath, err := filepath.Abs(basePath)
	if err != nil {
		return "", nil, fmt.Errorf("could not get absolute path for base directory: %w", err)
	}

	// Get the absolute path of the final joined path.
	absJoinedPath, err := filepath.Abs(joinedPath)
	if err != nil {
		return "", nil, fmt.Errorf("could not get absolute path for file: %w", err)
	}

	// The crucial security check: ensure the final path is within the base directory.
	if !isPathInsideBase(absBasePath, absJoinedPath) {
		return "", nil, errors.New("invalid file path: path is outside the allowed directory")
	}

	realBasePath, err := filepath.EvalSymlinks(absBasePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, config.ErrFileNotFound
		}
		return "", nil, fmt.Errorf("could not resolve base directory symlinks: %w", err)
	}

	realJoinedPath, err := filepath.EvalSymlinks(absJoinedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, config.ErrFileNotFound
		}
		return "", nil, fmt.Errorf("could not resolve file symlinks: %w", err)
	}

	// Re-check after resolving symlinks so links cannot escape the base directory.
	if !isPathInsideBase(realBasePath, realJoinedPath) {
		return "", nil, errors.New("invalid file path: path is outside the allowed directory")
	}

	mType, err := mimetype.DetectFile(realJoinedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, config.ErrFileNotFound
		}
		return "", nil, fmt.Errorf("could not detect file type: %w", err)
	}
	if mType == nil {
		return "", nil, errors.New("could not detect file type")
	}

	return absJoinedPath, mType, nil
}
