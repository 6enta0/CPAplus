package signature

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"
)

const (
	MaxGrokEncryptedContentLen          = 8 * 1024 * 1024
	MinGrokEncryptedContentDecodedLen   = 50
	MinGrokEncryptedContentEntropyRatio = 0.85
)

// InspectGrokEncryptedContent validates the transport envelope of an xAI
// encrypted_content value. It does not attempt to decrypt or authenticate it.
func InspectGrokEncryptedContent(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("empty Grok encrypted_content")
	}
	if value != raw {
		return 0, fmt.Errorf("Grok encrypted_content has leading or trailing whitespace")
	}
	if len(value) > MaxGrokEncryptedContentLen {
		return 0, fmt.Errorf("Grok encrypted_content exceeds maximum length")
	}
	if strings.HasPrefix(value, "gAAAA") {
		return 0, fmt.Errorf("Grok encrypted_content looks like a GPT reasoning signature")
	}
	if strings.Contains(value, "=") {
		return 0, fmt.Errorf("Grok encrypted_content must use unpadded base64")
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' {
			continue
		}
		return 0, fmt.Errorf("Grok encrypted_content contains an invalid base64 character")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return 0, fmt.Errorf("Grok encrypted_content base64 decode failed: %w", err)
	}
	if len(decoded) < MinGrokEncryptedContentDecodedLen {
		return 0, fmt.Errorf("Grok encrypted_content decoded payload is too short")
	}
	if byteEntropyRatio(decoded) < MinGrokEncryptedContentEntropyRatio {
		return 0, fmt.Errorf("Grok encrypted_content decoded payload entropy is too low")
	}
	return len(decoded), nil
}

func byteEntropyRatio(buf []byte) float64 {
	if len(buf) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range buf {
		counts[b]++
	}
	n := float64(len(buf))
	entropy := 0.0
	for _, count := range counts {
		if count == 0 {
			continue
		}
		p := float64(count) / n
		entropy -= p * math.Log2(p)
	}
	maxSymbols := len(buf)
	if maxSymbols > 256 {
		maxSymbols = 256
	}
	if maxSymbols <= 1 {
		return 0
	}
	return entropy / math.Log2(float64(maxSymbols))
}
