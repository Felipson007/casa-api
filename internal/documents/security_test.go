package documents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-playground/validator/v10"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// TestLocalStorage_Read_ConfinedToRoot is the regression test for the audit's F1: GET
// /api/documents/{id}/file used to run os.ReadFile on a caller-controlled path, so a document
// row pointing at "/proc/self/environ" (or "../../etc/passwd") leaked arbitrary files.
func TestLocalStorage_Read_ConfinedToRoot(t *testing.T) {
	root := t.TempDir()
	storage := NewLocalStorage(root, nil)
	ctx := context.Background()

	// A real file just outside the uploads root — must never be readable through the store.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0o600); err != nil {
		t.Fatalf("seeding outside file: %v", err)
	}

	for _, p := range []string{
		outside,
		filepath.Join(root, "..", "secret.txt"),
		filepath.Join(root, "Faturas", "..", "..", "secret.txt"),
		"/etc/passwd",
		"/proc/self/environ",
	} {
		_, err := storage.Read(ctx, p)
		if err == nil {
			t.Fatalf("Read(%q) should have been rejected, but it succeeded", p)
		}
		var appErr *apperr.Error
		if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
			t.Fatalf("Read(%q): expected a validation rejection, got %v", p, err)
		}
	}

	// A path that Store legitimately produced still round-trips.
	stored, err := storage.Store(ctx, OriginInvoice, FileTypePDF, []byte("%PDF-1.4\n..."))
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, err := storage.Read(ctx, stored); err != nil {
		t.Fatalf("Read of a Store-produced path failed: %v", err)
	}
}

// TestService_Create_IgnoresAnyPath_AndHasNoDownloadableFile confirms POST /api/documents can no
// longer bind a document to a file: Create takes metadata only and DownloadFile 404s.
func TestService_Create_IgnoresAnyPath_AndHasNoDownloadableFile(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, NewLocalStorage(t.TempDir(), nil), nil, nil, nil, nil, nil)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateDocumentRequest{
		OriginalFilename: "contract.pdf",
		FileType:         FileTypeOther,
		Origin:           OriginContract,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.HasFile {
		t.Fatalf("metadata-only Create must not report HasFile")
	}

	stored, err := repo.FindByID(ctx, created.ID)
	if err != nil || stored == nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !IsMetadataOnlyPath(stored.FilePath) {
		t.Fatalf("Create must persist the metadata sentinel, got FilePath=%q", stored.FilePath)
	}

	_, err = service.DownloadFile(ctx, created.ID)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("DownloadFile on a metadata-only doc should be NotFound, got %v", err)
	}
}

// TestExternalURL_Validation covers F3: the ExternalURL field (rendered as an <a href> by the
// frontend) must reject non-http(s) schemes at the request boundary...
func TestExternalURL_Validation(t *testing.T) {
	v := validator.New()
	bad := []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"not a url",
		"/relative/path",
	}
	for _, u := range bad {
		u := u
		if err := v.Struct(CreateDocumentRequest{
			OriginalFilename: "x", FileType: FileTypeOther, Origin: OriginContract, ExternalURL: &u,
		}); err == nil {
			t.Fatalf("ExternalURL %q should have failed validation", u)
		}
	}
	ok := "https://seguradora.example.com/apolice/123"
	if err := v.Struct(CreateDocumentRequest{
		OriginalFilename: "x", FileType: FileTypeOther, Origin: OriginContract, ExternalURL: &ok,
	}); err != nil {
		t.Fatalf("a plain https URL should validate, got %v", err)
	}
}

// ...and the classifier-derived value (which bypasses request validation) gets the same guard.
func TestSafeExternalURL(t *testing.T) {
	drop := []string{"javascript:alert(1)", "data:text/html,x", "  ", "ftp://host/x", "relative"}
	for _, s := range drop {
		s := s
		if got := safeExternalURL(&s); got != nil {
			t.Fatalf("safeExternalURL(%q) = %q, want nil", s, *got)
		}
	}
	keep := "https://example.com/x"
	if got := safeExternalURL(&keep); got == nil || *got != keep {
		t.Fatalf("safeExternalURL(%q) dropped a valid https URL", keep)
	}
	if safeExternalURL(nil) != nil {
		t.Fatalf("safeExternalURL(nil) must stay nil")
	}
}
