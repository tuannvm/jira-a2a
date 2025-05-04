package common

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// StringPtr returns a pointer to the given string
func StringPtr(s string) *string {
	return &s
}

// BoolPtr returns a pointer to the given bool
func BoolPtr(b bool) *bool {
	return &b
}

// ExtractJSON extracts JSON content from a text string
// It looks for content between { and } or [ and ] brackets
func ExtractJSON(text string) (string, error) {
	// Try to find JSON object
	objRegex := regexp.MustCompile(`(?s)\{.*\}`)
	objMatch := objRegex.FindString(text)
	if objMatch != "" {
		// Validate it's valid JSON
		var obj interface{}
		if err := json.Unmarshal([]byte(objMatch), &obj); err == nil {
			return objMatch, nil
		}
	}

	// Try to find JSON array
	arrRegex := regexp.MustCompile(`(?s)\[.*\]`)
	arrMatch := arrRegex.FindString(text)
	if arrMatch != "" {
		// Validate it's valid JSON
		var arr interface{}
		if err := json.Unmarshal([]byte(arrMatch), &arr); err == nil {
			return arrMatch, nil
		}
	}

	return "", fmt.Errorf("no valid JSON found in text")
}

// GetStringValue retrieves a string value from a map using multiple possible keys
// It tries each key in order and returns the first non-empty value found
func GetStringValue(data map[string]interface{}, keys ...string) (string, bool) {
	for _, key := range keys {
		if val, ok := data[key]; ok {
			if strVal, ok := val.(string); ok && strVal != "" {
				return strVal, true
			}
		}
	}
	return "", false
}

// Capitalize capitalizes the first letter of a string
func Capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ReturnJSONError writes a JSON error response with the given status code and message
func ReturnJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errorResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"code":    statusCode,
			"message": message,
		},
	}

	if err := json.NewEncoder(w).Encode(errorResponse); err != nil {
		// If JSON encoding fails, fall back to plain text
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(fmt.Sprintf("Error: %s", message)))
	}
}

// JoinStrings joins a slice of strings with the given separator
func JoinStrings(strs []string, separator string) string {
	result := ""
	for i, str := range strs {
		if i > 0 {
			result += separator
		}
		result += str
	}
	return result
}
