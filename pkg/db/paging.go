package db

import (
	"encoding/base64"
	"fmt"
)

// EncodeOffset converts an integer offset into a base64 encoded string token.
func EncodeOffset(offset int) string {
	if offset <= 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", offset)))
}

// DecodeOffset converts a base64 encoded string token back into an integer offset.
func DecodeOffset(token string) int {
	if token == "" {
		return 0
	}
	b, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}
	var offset int
	_, err = fmt.Sscanf(string(b), "%d", &offset)
	if err != nil {
		return 0
	}
	return offset
}
