package documents

import (
	"context"
	"testing"
)

// pdfWith builds a distinct-but-valid PDF signature per marker — the package's shared pdfBytes is
// a single fixed value, and this test needs two files that differ only in content.
func pdfWith(marker string) []byte {
	return append([]byte("%PDF-1.4\n"), []byte(marker)...)
}

// TestUpload_SameFileTwiceReturnsTheSameDocument pins the fix for a production incident: uploading
// one invoice twice created two documents, each extracted into its own full set of expense lines,
// so the competência month reported double (2026-09 read R$ 19.980,68 for a R$ 9.996,79
// statement). Re-uploading is the natural reflex when an upload looks like it did nothing —
// extraction runs in the background — so this path has to be idempotent.
func TestUpload_SameFileTwiceReturnsTheSameDocument(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, newFakeDrive(), nil, nil, nil, nil)
	ctx := context.Background()
	content := pdfWith("fatura itau setembro")

	first, err := service.Upload(ctx, UploadRequest{
		Content: content, ContentType: "application/pdf",
		OriginalFilename: "Fatura_Itau.pdf", Origin: OriginInvoice,
	})
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}

	second, err := service.Upload(ctx, UploadRequest{
		Content: content, ContentType: "application/pdf",
		OriginalFilename: "Fatura_Itau (1).pdf", Origin: OriginInvoice,
	})
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("re-uploading the same bytes created a second document (%s vs %s)", second.ID, first.ID)
	}
	if len(repo.byID) != 1 {
		t.Fatalf("expected exactly 1 stored document, got %d", len(repo.byID))
	}

	// A different file must still produce a distinct document.
	other, err := service.Upload(ctx, UploadRequest{
		Content: pdfWith("fatura itau outubro"), ContentType: "application/pdf",
		OriginalFilename: "Fatura_Itau.pdf", Origin: OriginInvoice,
	})
	if err != nil {
		t.Fatalf("third upload: %v", err)
	}
	if other.ID == first.ID {
		t.Fatal("a different file must not be treated as a duplicate")
	}
}
