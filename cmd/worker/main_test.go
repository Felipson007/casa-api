package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestAgeEncryptRoundTrip verifies the exact age.Encrypt/writer-Close usage runPgDump relies on:
// data written through the encrypting writer, once Close()d, decrypts back to the original bytes
// via the matching identity — this is the highest-risk part of the age-encryption change (an
// omitted Close() silently produces a truncated/corrupt ciphertext that only fails at restore
// time, not at backup time), so it's worth pinning down directly rather than only exercising it
// through the full pg_dump shell-out (which needs a real Postgres and pg_dump binary neither this
// package nor its Docker test image has).
func TestAgeEncryptRoundTrip(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generating identity: %v", err)
	}

	plaintext := []byte("this is a fake pg_dump payload, just needs to round-trip byte-for-byte")

	var ciphertext bytes.Buffer
	w, err := age.Encrypt(&ciphertext, identity.Recipient())
	if err != nil {
		t.Fatalf("age.Encrypt: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("writing plaintext: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing encrypting writer: %v", err)
	}

	r, err := age.Decrypt(&ciphertext, identity)
	if err != nil {
		t.Fatalf("age.Decrypt: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading decrypted plaintext: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-tripped plaintext mismatch:\n got:  %q\n want: %q", got, plaintext)
	}
}

// TestPruneOldBackups_KeepsNewestAcrossBothSuffixes covers the transition from plain ".dump" to
// encrypted ".dump.age" filenames — a mixed directory (some old plaintext dumps, some new
// encrypted ones) must still prune down to `keep` files total, oldest first, regardless of
// extension.
func TestPruneOldBackups_KeepsNewestAcrossBothSuffixes(t *testing.T) {
	dir := t.TempDir()

	names := []string{
		"atlas_20260101_010000.dump",     // oldest, plain — should be pruned
		"atlas_20260102_010000.dump",     // should be pruned
		"atlas_20260103_010000.dump.age", // newest 3 — should survive
		"atlas_20260104_010000.dump.age",
		"atlas_20260105_010000.dump.age",
		"notes.txt", // not a backup file — must be left alone
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
	}

	pruneOldBackups(dir, 3, discardLogger())

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir after prune: %v", err)
	}
	remaining := make(map[string]bool, len(entries))
	for _, e := range entries {
		remaining[e.Name()] = true
	}

	for _, want := range []string{"atlas_20260103_010000.dump.age", "atlas_20260104_010000.dump.age", "atlas_20260105_010000.dump.age", "notes.txt"} {
		if !remaining[want] {
			t.Errorf("expected %s to survive pruning, it didn't (remaining: %v)", want, remaining)
		}
	}
	for _, gone := range []string{"atlas_20260101_010000.dump", "atlas_20260102_010000.dump"} {
		if remaining[gone] {
			t.Errorf("expected %s to be pruned, it wasn't", gone)
		}
	}
}
