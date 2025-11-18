package utils

import (
	"fmt"
	"strconv"
)

// FormatEpochID formats an epoch ID consistently as an integer string
// This prevents scientific notation display in logs and API responses
func FormatEpochID(epochID interface{}) string {
	switch v := epochID.(type) {
	case string:
		// If it's already a string, check if it's in scientific notation
		if isScientificNotation(v) {
			if parsed, err := parseScientificNotation(v); err == nil {
				return fmt.Sprintf("%.0f", parsed)
			}
		}
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case int, int32, int64:
		return fmt.Sprintf("%d", v)
	case uint, uint32, uint64:
		return fmt.Sprintf("%d", v)
	default:
		// Fallback to string conversion
		return fmt.Sprintf("%v", v)
	}
}

// isScientificNotation checks if a string represents a number in scientific notation
func isScientificNotation(s string) bool {
	return len(s) > 0 && (s[len(s)-1] == 'e' || s[len(s)-1] == 'E')
}

// parseScientificNotation parses a scientific notation string to float64
func parseScientificNotation(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}