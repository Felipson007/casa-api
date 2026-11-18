package documents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/keyring"
)

// metadataFilePathPrefix marks a Document.FilePath as a metadata-only record (a manually entered
// contract/policy with no uploaded file). POST /api/documents stores exactly metadataFilePath —
// the endpoint takes metadata only, never a real path — so a caller can no longer point a
// document at an arbitrary file on disk (it used to be able to: GET /api/documents/{id}/file
// would then serve that file, e.g. /proc/self/environ). Older rows written by the frontend use
// "metadata://manual-contract", so any "metadata://" value counts as "no file".
const metadataFilePathPrefix = "metadata://"

// metadataFilePath is the exact sentinel Service.Create persists. NOT NULL in the schema, so it
// has to be a concrete string.
const metadataFilePath = metadataFilePathPrefix + "manual"

// IsMetadataOnlyPath reports whether path is the sentinel for a record with no downloadable file.
func IsMetadataOnlyPath(path string) bool {
	return strings.HasPrefix(path, metadataFilePathPrefix)
}

// encryptedMagic prefixes every file written by an encryption-capable Storage, so reads can tell
// an encrypted file from a legacy plaintext one. Files written before per-tenant encryption (and
// metadata-only records) have no magic and are returned as-is.
const encryptedMagic = "ATL1"

// fileAAD binds a file's ciphertext to its purpose. The tenant is already implied by the DEK, so
// the AAD only needs to distinguish file bytes from other uses of the same key.
var fileAAD = []byte("documents:file")

// CipherResolver returns the cipher for the current context (its tenant). A nil cipher means "no
// tenant / encryption not configured" and Storage falls back to plaintext (legacy behavior).
type CipherResolver interface {
	CipherForContext(ctx context.Context) (*keyring.Cipher, error)
}

// LocalStorage writes/reads/deletes files under a root directory, one subfolder per Origin. Files
// written for a tenant context are encrypted with that tenant's DEK (envelope format: magic ||
// nonce || ciphertext); files without the magic are legacy plaintext and returned verbatim, so the
// cutover is transparent. Every path Read/Delete accept is confined to rootDir (see
// resolveWithinRoot).
type LocalStorage struct {
	rootDir string
	ciphers CipherResolver
}

func NewLocalStorage(rootDir string, ciphers CipherResolver) *LocalStorage {
	return &LocalStorage{rootDir: rootDir, ciphers: ciphers}
}

// resolveWithinRoot returns the cleaned absolute form of p, guaranteeing it stays inside rootDir.
func (s *LocalStorage) resolveWithinRoot(p string) (string, error) {
	rootAbs, err := filepath.Abs(s.rootDir)
	if err != nil {
		return "", fmt.Errorf("documents: resolving uploads root: %w", err)
	}
	pAbs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("documents: resolving file path: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, pAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", apperr.Validation("the document's file path is outside the uploads directory")
	}
	return pAbs, nil
}

func (s *LocalStorage) cipherFor(ctx context.Context) (*keyring.Cipher, error) {
	if s.ciphers == nil {
		return nil, nil
	}
	return s.ciphers.CipherForContext(ctx)
}

// encryptForTenant wraps content with the tenant cipher when one is available.
func (s *LocalStorage) encryptForTenant(ctx context.Context, content []byte) ([]byte, error) {
	c, err := s.cipherFor(ctx)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return content, nil
	}
	sealed, err := c.Encrypt(content, fileAAD)
	if err != nil {
		return nil, err
	}
	return append([]byte(encryptedMagic), sealed...), nil
}

// decryptForTenant unwraps a file if it carries the magic; legacy plaintext passes through.
func (s *LocalStorage) decryptForTenant(ctx context.Context, data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(encryptedMagic)) {
		return data, nil
	}
	c, err := s.cipherFor(ctx)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("documents: file is encrypted but no tenant key is available")
	}
	return c.Decrypt(data[len(encryptedMagic):], fileAAD)
}

// subfolderFor mirrors Java's DocumentoService.subpastaPara — one folder per Origin.
func subfolderFor(origin Origin) string {
	switch origin {
	case OriginInvoice:
		return "Faturas"
	case OriginPaystub:
		return "Receitas"
	case OriginLabResult:
		return "Exames"
	case OriginPersonal:
		return "Pessoal"
	case OriginAsset:
		return "Bens"
	case OriginPet:
		return "Pets"
	case OriginInsurance:
		return "Seguros"
	case OriginWarranty:
		return "Garantias"
	case OriginTax:
		return "Fiscal"
	case OriginContract:
		return "Contratos"
	default:
		return "Outros"
	}
}

func extensionFor(fileType FileType) string {
	switch fileType {
	case FileTypePDF:
		return "pdf"
	case FileTypePNG:
		return "png"
	case FileTypeWEBP:
		return "webp"
	default:
		return "jpg"
	}
}

// Store writes content under {rootDir}/{subfolder-for-origin}/{random}.{ext}, encrypted with the
// tenant's key when available, and returns the path to persist as Document.FilePath.
func (s *LocalStorage) Store(ctx context.Context, origin Origin, fileType FileType, content []byte) (string, error) {
	dir := filepath.Join(s.rootDir, subfolderFor(origin))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("documents: creating upload directory: %w", err)
	}
	name, err := randomHexName()
	if err != nil {
		return "", err
	}
	data, err := s.encryptForTenant(ctx, content)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+"."+extensionFor(fileType))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("documents: writing uploaded file: %w", err)
	}
	return path, nil
}

// Read returns a locally-stored file's raw bytes, decrypting it when it carries the encryption
// magic. path must be a value previously returned by Store; it is confined to rootDir.
func (s *LocalStorage) Read(ctx context.Context, path string) ([]byte, error) {
	safe, err := s.resolveWithinRoot(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(safe)
	if err != nil {
		return nil, fmt.Errorf("documents: reading local file: %w", err)
	}
	return s.decryptForTenant(ctx, data)
}

// Delete removes a locally-stored file (silent no-op if already gone). Confined to rootDir.
func (s *LocalStorage) Delete(ctx context.Context, path string) error {
	safe, err := s.resolveWithinRoot(path)
	if err != nil {
		return err
	}
	if err := os.Remove(safe); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("documents: deleting local file: %w", err)
	}
	return nil
}

// EncryptLegacyFile encrypts a previously-stored plaintext file in place, returning true when it
// actually re-wrote the file. Idempotent: already-encrypted files (magic present) are skipped.
func (s *LocalStorage) EncryptLegacyFile(ctx context.Context, path string) (bool, error) {
	safe, err := s.resolveWithinRoot(path)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(safe)
	if err != nil {
		return false, fmt.Errorf("documents: reading legacy file: %w", err)
	}
	if bytes.HasPrefix(data, []byte(encryptedMagic)) {
		return false, nil
	}
	enc, err := s.encryptForTenant(ctx, data)
	if err != nil {
		return false, err
	}
	if bytes.Equal(enc, data) {
		return false, nil // no cipher available: nothing to do
	}
	if err := os.WriteFile(safe, enc, 0o644); err != nil {
		return false, fmt.Errorf("documents: encrypting legacy file: %w", err)
	}
	return true, nil
}

func randomHexName() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("documents: generating filename: %w", err)
	}
	return hex.EncodeToString(b), nil
}
