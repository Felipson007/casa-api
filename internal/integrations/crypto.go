package integrations

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

const ivSizeBytes = 12 // matches the old Java CredentialCryptoService's TAMANHO_IV_BYTES

// EncryptedValue is a plaintext credential encrypted at rest — mirrors the old Java
// CredentialCryptoService.EncryptedValue record exactly: AES-256-GCM, a random 12-byte IV per
// encryption, Go's default 128-bit GCM tag (same as the Java's explicit GCMParameterSpec(128, iv)).
type EncryptedValue struct {
	Ciphertext []byte
	IV         []byte
}

// loadKey reads APP_ENCRYPTION_KEY (base64, same as the Java @Value("${APP_ENCRYPTION_KEY:}")) —
// deliberately read fresh on every call rather than cached at package init, so the process doesn't
// need the key configured just to boot; it only fails at the moment a credential is actually
// encrypted/decrypted, same as the Java's exigirChaveConfigurada().
func loadKey() ([]byte, error) {
	raw := os.Getenv("APP_ENCRYPTION_KEY")
	if raw == "" {
		return nil, fmt.Errorf("integrations: APP_ENCRYPTION_KEY not configured — required to connect an external integration")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("integrations: APP_ENCRYPTION_KEY is not valid base64: %w", err)
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("integrations: constructing AES cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// Encrypt encrypts a plaintext credential (an OAuth refresh token, or a static API key/bot token
// depending on Type) with a freshly generated random IV.
func Encrypt(plaintext string) (EncryptedValue, error) {
	key, err := loadKey()
	if err != nil {
		return EncryptedValue{}, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return EncryptedValue{}, fmt.Errorf("integrations: constructing GCM: %w", err)
	}
	iv := make([]byte, ivSizeBytes)
	if _, err := rand.Read(iv); err != nil {
		return EncryptedValue{}, fmt.Errorf("integrations: generating IV: %w", err)
	}
	ciphertext := gcm.Seal(nil, iv, []byte(plaintext), nil)
	return EncryptedValue{Ciphertext: ciphertext, IV: iv}, nil
}

// Decrypt decrypts a value previously produced by Encrypt. Fails (rather than returning garbage)
// if the key is wrong or the data was tampered with — GCM is authenticated, same guarantee the
// Java version documents.
func Decrypt(ciphertext, iv []byte) (string, error) {
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", fmt.Errorf("integrations: constructing GCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("integrations: decrypting credential — wrong key or tampered data: %w", err)
	}
	return string(plaintext), nil
}
