// Package keyring implements the envelope-encryption key hierarchy used for tenant data
// (docs/tenant-self-service.md, "trusted operator" model):
//
//	master KEK (TENANT_MASTER_KEY, outside the database)  ->  wraps one DEK per tenant
//	tenant DEK                                             ->  encrypts that tenant's data
//
// Because the KEK never lives in Postgres, a database dump or backup alone cannot unwrap any
// tenant's DEK, and destroying a tenant's wrapped DEK irreversibly crypto-shreds their encrypted
// data. The KEK currently lives in the server's environment (homelab) so the operator CAN decrypt
// during processing; moving it to a KMS or to the client (BYOK / zero-knowledge) later is a
// key-management change, not a data-model change.
package keyring

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	// dekLen is AES-256.
	dekLen   = 32
	nonceLen = 12
	// dekAAD binds a wrapped DEK to its purpose, so a wrapped value cannot be replayed as some
	// other ciphertext.
	dekAAD = "atlas-tenant-dek-v1"
)

// Keyring wraps/unwraps tenant DEKs with the master KEK.
type Keyring struct {
	master cipher.AEAD
}

// New builds a Keyring from the base64-encoded master key. An empty string returns (nil, nil):
// "not configured", which callers treat as a hard error only when encryption is actually needed.
func New(masterB64 string) (*Keyring, error) {
	raw := strings.TrimSpace(masterB64)
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("keyring: master key is not valid base64: %w", err)
	}
	if len(key) != dekLen {
		return nil, fmt.Errorf("keyring: master key must decode to %d bytes (AES-256), got %d", dekLen, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("keyring: constructing master cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyring: constructing master GCM: %w", err)
	}
	return &Keyring{master: aead}, nil
}

// Configured reports whether a master key is available.
func (k *Keyring) Configured() bool { return k != nil && k.master != nil }

// GenerateDEK returns a fresh random AES-256 data-encryption key.
func GenerateDEK() ([]byte, error) {
	dek := make([]byte, dekLen)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("keyring: generating DEK: %w", err)
	}
	return dek, nil
}

// Wrap encrypts a tenant DEK under the master KEK. Output layout: nonce || GCM ciphertext.
func (k *Keyring) Wrap(dek []byte) ([]byte, error) {
	if !k.Configured() {
		return nil, errors.New("keyring: master key not configured")
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("keyring: generating wrap nonce: %w", err)
	}
	sealed := k.master.Seal(nil, nonce, dek, []byte(dekAAD))
	return append(nonce, sealed...), nil
}

// Unwrap decrypts a wrapped tenant DEK.
func (k *Keyring) Unwrap(wrapped []byte) ([]byte, error) {
	if !k.Configured() {
		return nil, errors.New("keyring: master key not configured")
	}
	if len(wrapped) < nonceLen {
		return nil, errors.New("keyring: wrapped DEK is too short")
	}
	nonce, ct := wrapped[:nonceLen], wrapped[nonceLen:]
	dek, err := k.master.Open(nil, nonce, ct, []byte(dekAAD))
	if err != nil {
		return nil, fmt.Errorf("keyring: unwrapping DEK (wrong master key or tampered data): %w", err)
	}
	return dek, nil
}

// Cipher encrypts/decrypts a single tenant's data with that tenant's DEK.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a tenant Cipher from a raw DEK.
func NewCipher(dek []byte) (*Cipher, error) {
	if len(dek) != dekLen {
		return nil, fmt.Errorf("keyring: DEK must be %d bytes, got %d", dekLen, len(dek))
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("keyring: constructing tenant cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("keyring: constructing tenant GCM: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext with the tenant DEK. aad should bind the ciphertext to its context
// (e.g. tenant id + field name) so it cannot be moved to another field/tenant. Layout: nonce || ct.
func (c *Cipher) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("keyring: generating nonce: %w", err)
	}
	return append(nonce, c.aead.Seal(nil, nonce, plaintext, aad)...), nil
}

// Decrypt opens a blob produced by Encrypt.
func (c *Cipher) Decrypt(blob, aad []byte) ([]byte, error) {
	if len(blob) < nonceLen {
		return nil, errors.New("keyring: ciphertext is too short")
	}
	nonce, ct := blob[:nonceLen], blob[nonceLen:]
	plaintext, err := c.aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("keyring: decrypting tenant data: %w", err)
	}
	return plaintext, nil
}
