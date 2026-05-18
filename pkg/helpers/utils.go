package helpers

import (
	"crypto/rand"
	"math"
	"math/big"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
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
	// Match anything that is NOT a Unicode letter, number, dash, dot, or underscore.
	// \p{L} matches any kind of letter from any language.
	// \p{N} matches any kind of numeric character in any script.
	illegalNameChars = regexp.MustCompile(`[^\p{L}\p{N}\.\-\_]`)

	// Match multiple consecutive underscores to clean up the resulting string.
	multipleUnderscores = regexp.MustCompile(`_+`)
)

// MakeSafeFilename sanitizes a string to be used as a safe filename, preserving Unicode.
func MakeSafeFilename(name string) string {
	// 1. Prevent path traversal by extracting only the base file name
	// (e.g., "../../secret.txt" becomes "secret.txt")
	name = filepath.Base(filepath.Clean(name))

	// 2. Replace illegal characters (like spaces, slashes, quotes) with an underscore
	safeName := illegalNameChars.ReplaceAllString(name, "_")

	// 3. Clean up multiple consecutive underscores for better readability
	safeName = multipleUnderscores.ReplaceAllString(safeName, "_")

	// 4. Trim leading/trailing dots, spaces, or underscores (Windows dislikes trailing dots)
	safeName = strings.Trim(safeName, " ._")

	// 5. Provide a fallback if the resulting string is empty
	if safeName == "" {
		return uuid.New().String()
	}

	// 6. Enforce a max length of 255 bytes (standard limit for ext4, NTFS, APFS)
	// We truncate by runes rather than bytes to prevent slicing a multibyte Unicode character in half.
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
