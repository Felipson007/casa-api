package documents

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/categories"
	"github.com/atlas/atlas-go/internal/expenses"
	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/income"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

// documentDateTime converts a request's *civildate.Date into the *time.Time the domain struct
// still uses internally — nil-safe, used for both DocumentDate and ExpirationDate on Create and
// Update. Mirrors expenses/income's originalDateTime helper. Not used by UploadRequest — that
// path parses its own "YYYY-MM-DD" multipart form field by hand (see handler.go), never through
// JSON.
func documentDateTime(d *civildate.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	// List returns documents newest-first, narrowed by any of personID/assetID/petID and, when
	// query is non-empty, to documents whose filename/description/institution/tags match it
	// case-insensitively.
	List(ctx context.Context, personID, assetID, petID *uuid.UUID, query string) ([]Document, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Document, error)
	Create(ctx context.Context, d Document) error
	Update(ctx context.Context, d Document) error
	Delete(ctx context.Context, id uuid.UUID) error

	// UpdateFilePath rewrites only a document's file_path — used by the one-time local→Drive
	// migration (Service.MigrateLocalFilesToDrive) without touching any other column.
	UpdateFilePath(ctx context.Context, id uuid.UUID, filePath string) error

	// UpdateExtraction writes only the itemized-extraction reconciliation columns (migration
	// 000013) — kept off Update so the generic metadata path never clobbers them. Written by
	// Service.Extract (verdict) and Service.Reconcile (user-confirmed total).
	UpdateExtraction(ctx context.Context, id uuid.UUID, res ExtractionResult) error

	// FindByContentHash returns the document with these exact bytes, or nil — backs Upload's
	// duplicate check (migration 000014).
	FindByContentHash(ctx context.Context, hash string) (*Document, error)
}

// Service holds the business rules. Every public method is O(1) database round-trips
// (docs/architecture-plan.md section 13). drive is nil-safe (DownloadFile 500s with a clear
// message rather than panicking) so tests and any caller that never uploads/downloads real files
// don't need to construct a real Drive client.
type Service struct {
	repo       Repository
	storage    *LocalStorage
	drive      DriveStorage
	pandora    *pandora.Service
	expenses   *expenses.Service
	income     *income.Service
	health     *health.Service
	categories *categories.Service
}

func NewService(repo Repository, storage *LocalStorage, drive DriveStorage, pandoraSvc *pandora.Service, expensesSvc *expenses.Service, incomeSvc *income.Service, healthSvc *health.Service, categoriesSvc ...*categories.Service) *Service {
	var categoryService *categories.Service
	if len(categoriesSvc) > 0 {
		categoryService = categoriesSvc[0]
	}
	return &Service{repo: repo, storage: storage, drive: drive, pandora: pandoraSvc, expenses: expensesSvc, income: incomeSvc, health: healthSvc, categories: categoryService}
}

// UploadRequest carries a real file's bytes plus the same metadata CreateDocumentRequest takes,
// minus FilePath/FileType (derived from the upload itself, never client-supplied).
type UploadRequest struct {
	Content          []byte
	ContentType      string
	OriginalFilename string
	PersonID         *uuid.UUID
	AssetID          *uuid.UUID
	PetID            *uuid.UUID
	Origin           Origin
	DocumentDate     *time.Time
	Tags             *string
	Description      *string
	TaxDocKind       *TaxDocKind
}

// Upload validates, stores and persists a new document. Global uploads are classified by Pandora;
// supported origins are then extracted asynchronously into their destination modules.
func (s *Service) Upload(ctx context.Context, req UploadRequest) (DocumentDTO, error) {
	if len(req.Content) == 0 {
		return DocumentDTO{}, apperr.Validation("select a file", "file")
	}
	if len(req.Content) > MaxUploadBytes {
		return DocumentDTO{}, apperr.Validation("the file must be at most 15MB", "file")
	}
	fileType, err := fileTypeForContentType(req.ContentType)
	if err != nil {
		return DocumentDTO{}, err
	}
	if err := validateSignature(req.Content, fileType); err != nil {
		return DocumentDTO{}, err
	}
	// Re-uploading a file already in the system returns the document that's already there instead
	// of creating a second one. Uploading the same invoice twice used to produce two documents,
	// each extracted into its own full set of expense lines, doubling that competência month
	// (production, 2026-08-28). Re-uploading is a natural reflex when an upload looks like it did
	// nothing — extraction runs in the background, so it often does — which makes this the likely
	// path, not an edge case. To re-run a failed extraction, use Reextract; that's what it's for.
	contentHash := sha256Hex(req.Content)
	if duplicate, err := s.repo.FindByContentHash(ctx, contentHash); err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: checking for a duplicate upload: %w", err)
	} else if duplicate != nil {
		log.Printf("documents.Upload: %q is byte-identical to document %s — returning that one instead of creating a duplicate", req.OriginalFilename, duplicate.ID)
		return toDTO(*duplicate), nil
	}
	// Uploads globais chegam como OUTRO: a Pandora tenta identificar o destino antes de salvar.
	// Falha de IA nunca bloqueia o envio; nesse caso o documento permanece no Inbox para revisão.
	var recognisedText string
	var classifiedMetadata classifiedDocumentMetadata
	if req.Origin == OriginOther && s.pandora != nil {
		classified, text := s.classifyOrigin(ctx, req.OriginalFilename, fileType, req.Content)
		recognisedText = text
		if classified.origin != OriginOther {
			req.Origin = classified.origin
		}
		if classified.summary != nil {
			req.Description = classified.summary
		}
		classifiedMetadata = classified
	}
	if s.drive == nil {
		return DocumentDTO{}, apperr.Internal(fmt.Errorf("documents: no google drive client configured"))
	}
	// New uploads always go to the tenant's own Google Drive (in the clear). If the tenant hasn't
	// connected one, fail with a clear, actionable message instead of silently writing to disk.
	fileID, err := s.drive.UploadFile(ctx, req.Origin, fileType, req.OriginalFilename, req.Content)
	if err != nil {
		var appErr *apperr.Error
		if errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound {
			return DocumentDTO{}, apperr.Validation("conecte sua conta do Google Drive em Configurações para enviar arquivos", "file")
		}
		if isDriveAuthError(err) {
			log.Printf("documents.Upload: google drive authorization failed: %v", err)
			return DocumentDTO{}, apperr.Validation("a conexão com o Google Drive expirou ou foi revogada — reconecte em Configurações e envie de novo", "file")
		}
		return DocumentDTO{}, apperr.Internal(err)
	}
	path := DrivePath(fileID)

	originalFilename := req.OriginalFilename
	if originalFilename == "" {
		originalFilename = "documento"
	}
	d := Document{
		ID: uuid.New(), PersonID: req.PersonID, OriginalFilename: originalFilename, FilePath: path,
		FileType: fileType, Origin: req.Origin, DocumentDate: req.DocumentDate, Tags: req.Tags,
		AssetID: req.AssetID, PetID: req.PetID, Description: req.Description,
		Institution: classifiedMetadata.institution, Amount: classifiedMetadata.amount,
		ExpirationDate: classifiedMetadata.expirationDate,
		// safeExternalURL: the classifier is an LLM reading a possibly-hostile PDF; a prompt-injected
		// "javascript:..." here would be rendered as an <a href> by the frontend (stored XSS).
		ExternalURL: safeExternalURL(classifiedMetadata.externalURL),
		Notes:       classifiedMetadata.notes, ContentHash: &contentHash,
		TaxDocKind: req.TaxDocKind,
	}
	ensureDocumentSearchMetadata(&d)
	if err := s.repo.Create(ctx, d); err != nil {
		_ = s.drive.DeleteFile(ctx, path) // don't leave an orphaned file if the DB row failed (e.g. FK violation)
		return DocumentDTO{}, err
	}
	persisted, err := s.repo.FindByID(ctx, d.ID)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: re-reading after upload: %w", err)
	}
	// Fire-and-forget auto-extraction (FATURA/HOLERITE/EXAME). Failures are logged, never surface
	// to the upload caller — the document is already stored; extraction just enriches it.
	if s.pandora != nil && (s.expenses != nil || s.income != nil || s.health != nil) {
		if recognisedText != "" {
			safeGo(ctx, func() { s.extractText(context.WithoutCancel(ctx), *persisted, recognisedText, req.Content) })
		} else {
			safeGo(ctx, func() { s.Extract(context.WithoutCancel(ctx), *persisted, req.Content) })
		}
	}
	return toDTO(*persisted), nil
}

// DownloadedFile is a document's raw bytes plus enough metadata to serve it back with a correct
// Content-Type and download filename.
type DownloadedFile struct {
	Content     []byte
	ContentType string
	Filename    string
}

var contentTypeByFileType = map[FileType]string{
	FileTypePDF:  "application/pdf",
	FileTypePNG:  "image/png",
	FileTypeJPEG: "image/jpeg",
	FileTypeWEBP: "image/webp",
}

// DownloadFile returns a document's raw bytes — from Google Drive if FilePath was migrated from
// the old production storage (still "googledrive:{fileId}"), from local disk otherwise (anything
// uploaded through this Go backend). Returns apperr.NotFound if the document itself doesn't exist.
func (s *Service) DownloadFile(ctx context.Context, id uuid.UUID) (DownloadedFile, error) {
	d, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("documents: looking up for download: %w", err)
	}
	if d == nil {
		return DownloadedFile{}, apperr.NotFound("document not found")
	}
	if IsMetadataOnlyPath(d.FilePath) {
		return DownloadedFile{}, apperr.NotFound("this document has no file to download")
	}

	var content []byte
	if IsGoogleDrivePath(d.FilePath) {
		if s.drive == nil {
			return DownloadedFile{}, apperr.Internal(fmt.Errorf("documents: no google drive client configured"))
		}
		content, err = s.drive.FetchFile(ctx, d.FilePath)
	} else {
		if s.storage == nil {
			return DownloadedFile{}, apperr.Internal(fmt.Errorf("documents: no local storage configured"))
		}
		content, err = s.storage.Read(ctx, d.FilePath)
	}
	if err != nil {
		return DownloadedFile{}, err
	}

	contentType := contentTypeByFileType[d.FileType]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return DownloadedFile{Content: content, ContentType: contentType, Filename: d.OriginalFilename}, nil
}

// List returns documents, optionally filtered by person, asset, and/or pet.
func (s *Service) List(ctx context.Context, personID, assetID, petID *uuid.UUID, query string) ([]DocumentDTO, error) {
	list, err := s.repo.List(ctx, personID, assetID, petID, query)
	if err != nil {
		return nil, fmt.Errorf("documents: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single document, or apperr.NotFound if it doesn't exist.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (DocumentDTO, error) {
	d, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: looking up: %w", err)
	}
	if d == nil {
		return DocumentDTO{}, apperr.NotFound("document not found")
	}
	return toDTO(*d), nil
}

// Create persists new document metadata. This is the no-file path (a manually entered contract/
// policy); FilePath is always the "metadata://" sentinel, never a caller value — see
// CreateDocumentRequest's doc comment.
func (s *Service) Create(ctx context.Context, req CreateDocumentRequest) (DocumentDTO, error) {
	d := Document{
		ID:               uuid.New(),
		PersonID:         req.PersonID,
		OriginalFilename: req.OriginalFilename,
		FilePath:         metadataFilePath,
		FileType:         req.FileType,
		Origin:           req.Origin,
		DocumentDate:     documentDateTime(req.DocumentDate),
		Tags:             req.Tags,
		AssetID:          req.AssetID,
		PetID:            req.PetID,
		Description:      req.Description,
		Institution:      req.Institution,
		Amount:           req.Amount,
		ExpirationDate:   documentDateTime(req.ExpirationDate),
		ExternalURL:      req.ExternalURL,
		Notes:            req.Notes,
		TaxDocKind:       req.TaxDocKind,
	}
	ensureDocumentSearchMetadata(&d)
	if err := s.repo.Create(ctx, d); err != nil {
		return DocumentDTO{}, err // already an *apperr.Error (FK violation) or a wrapped DB error
	}
	// Re-read rather than mapping the in-memory `d` directly: CreatedAt is a DB-generated
	// DEFAULT now(), never set above (found systemically across every Create in this codebase by
	// Hermes's HTTP endpoint sweep on loans, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, d.ID)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// Update replaces a document's editable metadata (person/asset/pet links, document date, tags) —
// see UpdateDocumentRequest's doc comment for why FilePath/FileType/OriginalFilename/Origin aren't
// re-settable here. Returns apperr.NotFound if the document doesn't exist.
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateDocumentRequest) (DocumentDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: looking up for update: %w", err)
	}
	if existing == nil {
		return DocumentDTO{}, apperr.NotFound("document not found")
	}

	updated := Document{
		ID:               id,
		PersonID:         req.PersonID,
		OriginalFilename: existing.OriginalFilename,
		FilePath:         existing.FilePath,
		FileType:         existing.FileType,
		Origin:           existing.Origin,
		DocumentDate:     documentDateTime(req.DocumentDate),
		Tags:             req.Tags,
		AssetID:          req.AssetID,
		PetID:            req.PetID,
		Description:      req.Description,
		Institution:      req.Institution,
		Amount:           req.Amount,
		ExpirationDate:   documentDateTime(req.ExpirationDate),
		ExternalURL:      req.ExternalURL,
		Notes:            req.Notes,
		CreatedAt:        existing.CreatedAt,
		TaxDocKind:       req.TaxDocKind,
	}
	if err := s.repo.Update(ctx, updated); err != nil {
		return DocumentDTO{}, err
	}
	return toDTO(updated), nil
}

// Classify changes only the logical destination of a document. Keeping every other field from the
// persisted row makes Inbox acceptance safe even when the document already has enriched metadata.
func (s *Service) Classify(ctx context.Context, id uuid.UUID, origin Origin) (DocumentDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return DocumentDTO{}, fmt.Errorf("documents: looking up for classification: %w", err)
	}
	if existing == nil {
		return DocumentDTO{}, apperr.NotFound("document not found")
	}
	existing.Origin = origin
	ensureDocumentSearchMetadata(existing)
	if err := s.repo.Update(ctx, *existing); err != nil {
		return DocumentDTO{}, err
	}
	return toDTO(*existing), nil
}

// DriveMigrationResult summarizes Service.MigrateLocalFilesToDrive.
type DriveMigrationResult struct {
	// Scanned is every document of the tenant; Migrated/Pending are disjoint; Skipped counts
	// metadata-only and already-on-Drive rows.
	Scanned  int
	Migrated int
	Pending  int // dry-run only: files that WOULD be migrated
	Skipped  int
	Failures []DriveMigrationFailure
}

// DriveMigrationFailure records one document whose file could not be moved to Drive.
type DriveMigrationFailure struct {
	DocumentID       uuid.UUID
	OriginalFilename string
	Reason           string
}

// MigrateLocalFilesToDrive uploads every locally-stored file of the context's tenant to that
// tenant's Google Drive, rewriting FilePath to "googledrive:{id}". The local copy is left in place
// (nothing is deleted), so the migration is reversible. dryRun reports what would move without
// uploading. Documents already on Drive or metadata-only are skipped; a file missing from disk is
// recorded as a failure and the sweep continues.
func (s *Service) MigrateLocalFilesToDrive(ctx context.Context, dryRun bool) (DriveMigrationResult, error) {
	var res DriveMigrationResult
	docs, err := s.repo.List(ctx, nil, nil, nil, "")
	if err != nil {
		return res, err
	}
	res.Scanned = len(docs)
	if s.storage == nil || s.drive == nil {
		return res, apperr.Internal(fmt.Errorf("documents: migration requires local storage and a google drive client"))
	}
	for _, d := range docs {
		if d.FilePath == "" || IsMetadataOnlyPath(d.FilePath) || IsGoogleDrivePath(d.FilePath) {
			res.Skipped++
			continue
		}
		content, err := s.storage.Read(ctx, d.FilePath)
		if err != nil {
			res.Failures = append(res.Failures, DriveMigrationFailure{d.ID, d.OriginalFilename, err.Error()})
			continue
		}
		if dryRun {
			res.Pending++
			continue
		}
		fileID, err := s.drive.UploadFile(ctx, d.Origin, d.FileType, d.OriginalFilename, content)
		if err != nil {
			res.Failures = append(res.Failures, DriveMigrationFailure{d.ID, d.OriginalFilename, err.Error()})
			continue
		}
		if err := s.repo.UpdateFilePath(ctx, d.ID, DrivePath(fileID)); err != nil {
			res.Failures = append(res.Failures, DriveMigrationFailure{d.ID, d.OriginalFilename, err.Error()})
			continue
		}
		res.Migrated++
	}
	return res, nil
}

// EncryptLegacyFiles encrypts any plaintext local file belonging to the context's tenant,
// returning how many were re-written. Idempotent (already-encrypted files are skipped); used once
// after per-tenant file encryption is enabled, to bring pre-existing uploads under the DEK.
func (s *Service) EncryptLegacyFiles(ctx context.Context) (int, error) {
	docs, err := s.repo.List(ctx, nil, nil, nil, "")
	if err != nil {
		return 0, err
	}
	encrypted := 0
	for _, d := range docs {
		if d.FilePath == "" || IsMetadataOnlyPath(d.FilePath) {
			continue
		}
		changed, err := s.storage.EncryptLegacyFile(ctx, d.FilePath)
		if err != nil {
			// Skip, don't abort, files that aren't local uploads (Google Drive paths) or are
			// already gone (the pre-PVC emptyDir incident). Only a real I/O failure aborts.
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			var appErr *apperr.Error
			if errors.As(err, &appErr) && appErr.Kind == apperr.KindValidation {
				continue
			}
			return encrypted, err
		}
		if changed {
			encrypted++
		}
	}
	return encrypted, nil
}

// FailInterruptedExtractions marks every extraction still PENDING as FAILED. Extractions run as
// goroutines inside the api process, so at startup none can still be running: a PENDING left
// behind was killed by a restart and would otherwise show "lendo lançamentos" forever. The note
// tells the user to retry (the Inbox's "Tentar novamente" re-runs it).
func (s *Service) FailInterruptedExtractions(ctx context.Context) (int, error) {
	docs, err := s.repo.List(ctx, nil, nil, nil, "")
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, d := range docs {
		if d.ExtractionStatus == nil || *d.ExtractionStatus != ExtractionPending {
			continue
		}
		note := "A leitura foi interrompida (o servidor reiniciou). Use \"Tentar novamente\"."
		if err := s.repo.UpdateExtraction(ctx, d.ID, ExtractionResult{Status: ExtractionFailed, Note: &note}); err != nil {
			return failed, err
		}
		failed++
	}
	return failed, nil
}

// Delete removes a document's metadata row and the underlying file — from Google Drive for a
// "googledrive:"-backed row, from local disk for a legacy local one. Returns apperr.NotFound if the
// document doesn't exist.
//
// For an INVOICE or PAYSTUB, the expense/income lines that auto-extraction created FROM this
// document go with it. The FK is ON DELETE SET NULL, which is right for a hand-entered expense
// that merely cites a document — but an extracted statement line has no independent existence,
// and leaving those behind is silently wrong: deleting a duplicated invoice from the UI removed
// the document while its 173 lines stayed in the month, so the total never corrected (production,
// 2026-08-28 — September read R$ 19.980,68 for a R$ 9.996,79 statement). Deleting a statement
// therefore means deleting what was read out of it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("documents: looking up for delete: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("document not found")
	}
	if err := s.deleteExtractedLines(ctx, *existing); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("documents: deleting: %w", err)
	}
	switch {
	case IsMetadataOnlyPath(existing.FilePath):
		// nothing on disk or Drive
	case IsGoogleDrivePath(existing.FilePath):
		if s.drive != nil {
			if err := s.drive.DeleteFile(ctx, existing.FilePath); err != nil {
				return fmt.Errorf("documents: deleting google drive file: %w", err)
			}
		}
	default:
		if s.storage != nil {
			if err := s.storage.Delete(ctx, existing.FilePath); err != nil {
				return fmt.Errorf("documents: deleting underlying file: %w", err)
			}
		}
	}
	return nil
}

// deleteExtractedLines drops every expense/income line auto-extraction produced from doc.
// ReplaceForDocument with no replacements is exactly "delete this document's lines", in the same
// single transaction the import itself uses.
func (s *Service) deleteExtractedLines(ctx context.Context, doc Document) error {
	switch doc.Origin {
	case OriginInvoice:
		if s.expenses == nil {
			return nil
		}
		if _, err := s.expenses.ReplaceForDocument(ctx, doc.ID, nil); err != nil {
			return fmt.Errorf("documents: deleting extracted expense lines: %w", err)
		}
	case OriginPaystub:
		if s.income == nil {
			return nil
		}
		if _, err := s.income.ReplaceForDocument(ctx, doc.ID, nil); err != nil {
			return fmt.Errorf("documents: deleting extracted income lines: %w", err)
		}
	}
	return nil
}
