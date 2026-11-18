package documents

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/keyring"
)

type fakeCipherResolver struct{ c *keyring.Cipher }

func (f fakeCipherResolver) CipherForContext(context.Context) (*keyring.Cipher, error) {
	return f.c, nil
}

func TestLocalStorage_EncryptsFilesWithTenantCipher(t *testing.T) {
	c, err := keyring.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	root := t.TempDir()
	storage := NewLocalStorage(root, fakeCipherResolver{c})
	ctx := context.Background()

	path, err := storage.Store(ctx, OriginInvoice, FileTypePDF, []byte("secret-bytes"))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading stored file: %v", err)
	}
	if bytes.Contains(raw, []byte("secret-bytes")) {
		t.Fatal("file was stored in clear")
	}
	if !bytes.HasPrefix(raw, []byte(encryptedMagic)) {
		t.Fatal("file is missing the encryption magic")
	}
	got, err := storage.Read(ctx, path)
	if err != nil || string(got) != "secret-bytes" {
		t.Fatalf("Read = (%q, %v), want secret-bytes", got, err)
	}
}

func TestLocalStorage_LegacyPlaintextAndBackfill(t *testing.T) {
	c, _ := keyring.NewCipher(bytes.Repeat([]byte{3}, 32))
	root := t.TempDir()
	storage := NewLocalStorage(root, fakeCipherResolver{c})
	ctx := context.Background()

	legacy := filepath.Join(root, "legacy.bin")
	if err := os.WriteFile(legacy, []byte("plain-content"), 0o644); err != nil {
		t.Fatalf("seeding legacy file: %v", err)
	}
	// Legacy plaintext is returned as-is.
	got, err := storage.Read(ctx, legacy)
	if err != nil || string(got) != "plain-content" {
		t.Fatalf("legacy Read = (%q, %v)", got, err)
	}
	// Backfill encrypts it in place...
	changed, err := storage.EncryptLegacyFile(ctx, legacy)
	if err != nil || !changed {
		t.Fatalf("EncryptLegacyFile = (%v, %v), want changed", changed, err)
	}
	raw, _ := os.ReadFile(legacy)
	if bytes.Contains(raw, []byte("plain-content")) {
		t.Fatal("legacy file still in clear after backfill")
	}
	// ...and it still reads back correctly, and is idempotent.
	if got, err := storage.Read(ctx, legacy); err != nil || string(got) != "plain-content" {
		t.Fatalf("post-backfill Read = (%q, %v)", got, err)
	}
	if changed, _ := storage.EncryptLegacyFile(ctx, legacy); changed {
		t.Fatal("EncryptLegacyFile should be a no-op on an already-encrypted file")
	}
}
