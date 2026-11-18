package documents

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// safeExternalURL returns u only when it is a well-formed http/https URL; anything else
// (javascript:, data:, a bare word, a relative path, ...) becomes nil. The HTTP layer already
// enforces this on the Create/Update request bodies via the "http_url" validate tag; this is the
// same guard for the one path that bypasses that validation — the value the document classifier
// (internal/documents.classifyOrigin) proposes from arbitrary uploaded content.
func safeExternalURL(u *string) *string {
	if u == nil {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(*u))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil
	}
	return u
}

// MaxUploadBytes mirrors Java's ARQUIVO_MAX_BYTES (15MB).
const MaxUploadBytes = 15 * 1024 * 1024

var (
	pdfMagic  = []byte{'%', 'P', 'D', 'F', '-'}
	pngMagic  = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	riffMagic = []byte{'R', 'I', 'F', 'F'}
)

// fileTypeForContentType mirrors Java's upload switch on the declared Content-Type — only these
// four are accepted, anything else is rejected before even reading the body's magic bytes.
func fileTypeForContentType(contentType string) (FileType, error) {
	switch contentType {
	case "application/pdf":
		return FileTypePDF, nil
	case "image/png":
		return FileTypePNG, nil
	case "image/jpeg":
		return FileTypeJPEG, nil
	case "image/webp":
		return FileTypeWEBP, nil
	default:
		return "", apperr.Validation("unsupported format — use PDF, PNG, JPEG, or WEBP", "file")
	}
}

// validateSignature confirms content's real magic bytes match the declared FileType — mirrors
// Java's validarAssinaturaDeArquivo exactly: the client-declared Content-Type is freely chosen by
// the caller, magic bytes confirm what the file actually is.
func validateSignature(content []byte, fileType FileType) error {
	var ok bool
	switch fileType {
	case FileTypePDF:
		ok = bytes.HasPrefix(content, pdfMagic)
	case FileTypePNG:
		ok = bytes.HasPrefix(content, pngMagic)
	case FileTypeJPEG:
		ok = bytes.HasPrefix(content, jpegMagic)
	case FileTypeWEBP:
		ok = len(content) >= 12 && bytes.HasPrefix(content, riffMagic) &&
			content[8] == 'W' && content[9] == 'E' && content[10] == 'B' && content[11] == 'P'
	}
	if !ok {
		return apperr.Validation("the uploaded file's content doesn't match its declared format", "file")
	}
	return nil
}
