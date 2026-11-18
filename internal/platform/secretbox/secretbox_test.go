package secretbox

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x2a}, 32))
}

func TestRoundTrip(t *testing.T) {
	box, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !box.Configured() {
		t.Fatal("expected box to be configured")
	}
	plaintext := []byte("JBSWY3DPEHPK3PXP")
	aad := []byte("atlas-mfa-secret-v1:user-1")
	sealed, err := box.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("ciphertext must not contain the plaintext secret")
	}
	opened, err := box.Open(sealed, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("roundtrip mismatch: got %q want %q", opened, plaintext)
	}
}

func TestOpenRejectsWrongAAD(t *testing.T) {
	box, _ := New(testKey(t))
	sealed, err := box.Seal([]byte("secret"), []byte("aad-a"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := box.Open(sealed, []byte("aad-b")); err == nil {
		t.Fatal("expected Open to fail with a different AAD")
	}
}

func TestUnconfigured(t *testing.T) {
	box, err := New("   ")
	if err != nil {
		t.Fatalf("New(empty): %v", err)
	}
	if box != nil {
		t.Fatal("expected nil box for empty key")
	}
	if box.Configured() {
		t.Fatal("nil box must not report configured")
	}
	if _, err := box.Seal([]byte("x"), nil); err == nil {
		t.Fatal("Seal on nil box must error")
	}
	if _, err := box.Open([]byte("x"), nil); err == nil {
		t.Fatal("Open on nil box must error")
	}
}

func TestNewRejectsBadBase64(t *testing.T) {
	if _, err := New("not base64!!!"); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}
