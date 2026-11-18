package config

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func TestValidateEncryptionKey(t *testing.T) {
	key32 := make([]byte, 32)
	_, _ = rand.Read(key32)
	good := base64.StdEncoding.EncodeToString(key32)

	t.Run("empty is allowed", func(t *testing.T) {
		if err := validateEncryptionKey(""); err != nil {
			t.Fatalf("empty key must be allowed (no integrations configured), got %v", err)
		}
		if err := validateEncryptionKey("   "); err != nil {
			t.Fatalf("blank key must be allowed, got %v", err)
		}
	})

	t.Run("valid 32-byte base64 passes", func(t *testing.T) {
		if err := validateEncryptionKey(good); err != nil {
			t.Fatalf("a real 32-byte base64 key must pass, got %v", err)
		}
	})

	t.Run("placeholder rejected", func(t *testing.T) {
		for _, p := range []string{"change-me", "changeme"} {
			if err := validateEncryptionKey(p); err == nil {
				t.Fatalf("placeholder %q must be rejected", p)
			}
		}
	})

	t.Run("non-base64 rejected", func(t *testing.T) {
		if err := validateEncryptionKey("not@@base64!!"); err == nil {
			t.Fatal("non-base64 must be rejected")
		}
	})

	t.Run("wrong length rejected", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString([]byte("only-13-bytes!"))
		err := validateEncryptionKey(short)
		if err == nil || !strings.Contains(err.Error(), "16, 24 or 32") {
			t.Fatalf("a base64 value that isn't an AES key length must be rejected, got %v", err)
		}
	})
}
