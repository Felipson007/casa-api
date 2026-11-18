// Package secretbox encrypts small application secrets (today: each user's TOTP shared secret)
// with a key derived from APP_ENCRYPTION_KEY. It reuses keyring's AES-256-GCM primitives: the
// configured key is hashed with SHA-256 first, so any of the key sizes APP_ENCRYPTION_KEY already
// accepts (16/24/32 bytes) maps to one fixed-size AES-256 key.
//
// The point is that a database dump or backup alone is not enough to mint valid MFA codes: the
// ciphertext is useless without the key, which lives in the server environment.
package secretbox

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/atlas/atlas-go/internal/platform/keyring"
)

// Box is a configured (or not) secret cipher. A nil, or a Box built from an empty key, is
// "not configured": callers must treat any operation as an error rather than storing plaintext.
type Box struct {
	cipher *keyring.Cipher
}

// New derives a Box from the base64-encoded APP_ENCRYPTION_KEY. An empty value returns (nil, nil) —
// "not configured", which MFA setup treats as a hard error instead of silently storing a
// decryptable secret.
func New(masterB64 string) (*Box, error) {
	raw := strings.TrimSpace(masterB64)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("secretbox: key is not valid base64: %w", err)
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("secretbox: key decodes to zero bytes")
	}
	sum := sha256.Sum256(decoded)
	cipher, err := keyring.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("secretbox: building cipher: %w", err)
	}
	return &Box{cipher: cipher}, nil
}

// Configured reports whether a key is available.
func (b *Box) Configured() bool { return b != nil && b.cipher != nil }

// Seal encrypts plaintext, binding the ciphertext to aad so it cannot be replayed in another
// context (e.g. moved from one user's MFA secret to another's).
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	if !b.Configured() {
		return nil, fmt.Errorf("secretbox: APP_ENCRYPTION_KEY is not configured")
	}
	return b.cipher.Encrypt(plaintext, aad)
}

// Open decrypts a blob produced by Seal with the same aad.
func (b *Box) Open(blob, aad []byte) ([]byte, error) {
	if !b.Configured() {
		return nil, fmt.Errorf("secretbox: APP_ENCRYPTION_KEY is not configured")
	}
	return b.cipher.Decrypt(blob, aad)
}
