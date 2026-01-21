package helpers

import (
	"crypto/rand"
	"math"
	"math/big"
	"strings"
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
