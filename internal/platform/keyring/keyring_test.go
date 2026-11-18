package keyring

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testMaster(t *testing.T) *Keyring {
	t.Helper()
	k, err := New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return k
}

func TestNewRejectsBadMasterKey(t *testing.T) {
	if k, err := New(""); err != nil || k != nil {
		t.Fatalf("empty master key should be (nil, nil), got (%v, %v)", k, err)
	}
	if _, err := New("not-base64!!"); err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if _, err := New(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected error for wrong key length")
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	k := testMaster(t)
	dek, err := GenerateDEK()
	if err != nil {
		t.Fatalf("GenerateDEK: %v", err)
	}
	wrapped, err := k.Wrap(dek)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	if bytes.Contains(wrapped, dek) {
		t.Fatal("wrapped DEK must not contain the plaintext DEK")
	}
	got, err := k.Unwrap(wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatal("unwrapped DEK differs from original")
	}
}

func TestUnwrapWithWrongMasterKeyFails(t *testing.T) {
	k1 := testMaster(t)
	other, err := New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dek, _ := GenerateDEK()
	wrapped, _ := k1.Wrap(dek)
	if _, err := other.Unwrap(wrapped); err == nil {
		t.Fatal("expected unwrap to fail with a different master key")
	}
}

func TestUnwrapTamperedFails(t *testing.T) {
	k := testMaster(t)
	dek, _ := GenerateDEK()
	wrapped, _ := k.Wrap(dek)
	wrapped[len(wrapped)-1] ^= 0xff
	if _, err := k.Unwrap(wrapped); err == nil {
		t.Fatal("expected unwrap to fail on tampered ciphertext")
	}
}

func TestCipherAADBinding(t *testing.T) {
	dek, _ := GenerateDEK()
	c, err := NewCipher(dek)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	blob, err := c.Encrypt([]byte("secret"), []byte("tenant-a:field"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := c.Decrypt(blob, []byte("tenant-a:field"))
	if err != nil || string(got) != "secret" {
		t.Fatalf("Decrypt = (%q, %v), want (secret, nil)", got, err)
	}
	if _, err := c.Decrypt(blob, []byte("tenant-b:field")); err == nil {
		t.Fatal("expected decrypt to fail with a different AAD")
	}
}
