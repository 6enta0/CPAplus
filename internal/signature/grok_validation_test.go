package signature

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestInspectGrokEncryptedContent(t *testing.T) {
	buf := make([]byte, 0, 128)
	for i := 0; len(buf) < 128; i++ {
		sum := sha256.Sum256([]byte{byte(i), 7})
		buf = append(buf, sum[:]...)
	}
	valid := base64.RawStdEncoding.EncodeToString(buf[:128])
	if decodedLen, err := InspectGrokEncryptedContent(valid); err != nil || decodedLen != 128 {
		t.Fatalf("InspectGrokEncryptedContent(valid) = %d, %v", decodedLen, err)
	}
	for _, invalid := range []string{"", "bad", " gAAAAABforeign", "gAAAAABforeign", "abc_def"} {
		if _, err := InspectGrokEncryptedContent(invalid); err == nil {
			t.Fatalf("InspectGrokEncryptedContent(%q) unexpectedly passed", invalid)
		}
	}
}
