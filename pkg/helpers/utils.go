package helpers

import "math"

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
