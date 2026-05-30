package helpers

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
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
	// Create a slice with all possible digits.
	digits := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}

	// Fisher-Yates shuffle using crypto/rand for secure randomness.
	for i := len(digits) - 1; i > 0; i-- {
		// Generate a cryptographically secure random number j such that 0 <= j <= i.
		num, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			// Fallback to a less secure but still random shuffle on error.
			// This should ideally be logged as a critical failure.
			return GenerateSipPin(length) // Simple recursion for fallback
		}
		j := int(num.Int64())
		digits[i], digits[j] = digits[j], digits[i]
	}

	// Take the first "length" digits from the shuffled slice to guarantee uniqueness.
	pinDigits := digits[:length]

	// Use a strings.Builder for efficient string concatenation.
	var builder strings.Builder
	builder.Grow(length)
	for _, d := range pinDigits {
		builder.WriteRune(rune('0' + d))
	}
	return builder.String()
}

var (
	// A minimal regex to remove characters that are illegal in Windows filenames,
	// plus the null byte which is a general security risk.
	// We explicitly include / and \ even though filepath.Base handles them, for clarity.
	minimalIllegalChars = regexp.MustCompile(`[<>:"/\\|?*\x00]`)
)

// MakeSafeFilename sanitizes a string to be used as a safe filename.
// It is designed to be as permissive as possible, only removing characters
// that are illegal on major filesystems or pose a security risk.
func MakeSafeFilename(name string, fallbackToUUID bool) string {
	// 1. Prevent path traversal. This is the most important security step.
	name = filepath.Base(filepath.Clean(name))

	// 2. Replace the minimal set of illegal/unsafe characters with an underscore.
	safeName := minimalIllegalChars.ReplaceAllString(name, "_")

	// 3. Trim leading/trailing dots and spaces, which can cause issues on Windows.
	safeName = strings.Trim(safeName, " .")

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
	if !strings.HasPrefix(absJoinedPath, absBasePath) || absJoinedPath == absBasePath {
		return "", nil, errors.New("invalid file path: path is outside the allowed directory")
	}

	// Now that the path is validated, detect the MIME type.
	mType, err := mimetype.DetectFile(absJoinedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, config.ErrFileNotFound
		}
		return "", nil, fmt.Errorf("could not detect file type: %w", err)
	}

	return absJoinedPath, mType, nil
}
