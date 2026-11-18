package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// fakeRepository is an in-memory Repository used by unit tests — no Postgres involved.
type fakeRepository struct {
	byID map[uuid.UUID]Document
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{byID: make(map[uuid.UUID]Document)}
}

func (f *fakeRepository) List(_ context.Context, personID, assetID, petID *uuid.UUID, query string) ([]Document, error) {
	var result []Document
	for _, d := range f.byID {
		if personID != nil && (d.PersonID == nil || *d.PersonID != *personID) {
			continue
		}
		if assetID != nil && (d.AssetID == nil || *d.AssetID != *assetID) {
			continue
		}
		if petID != nil && (d.PetID == nil || *d.PetID != *petID) {
			continue
		}
		if query != "" && !documentMatchesQuery(d, query) {
			continue
		}
		result = append(result, d)
	}
	return result, nil
}

func documentMatchesQuery(d Document, query string) bool {
	q := strings.ToLower(query)
	if strings.Contains(strings.ToLower(d.OriginalFilename), q) {
		return true
	}
	for _, field := range []*string{d.Description, d.Institution, d.Tags} {
		if field != nil && strings.Contains(strings.ToLower(*field), q) {
			return true
		}
	}
	return false
}

func (f *fakeRepository) FindByID(_ context.Context, id uuid.UUID) (*Document, error) {
	d, ok := f.byID[id]
	if !ok {
		return nil, nil
	}
	return &d, nil
}

func (f *fakeRepository) Create(_ context.Context, d Document) error {
	f.byID[d.ID] = d
	return nil
}

func (f *fakeRepository) Update(_ context.Context, d Document) error {
	if _, ok := f.byID[d.ID]; !ok {
		return errors.New("not found")
	}
	f.byID[d.ID] = d
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id uuid.UUID) error {
	delete(f.byID, id)
	return nil
}

func (f *fakeRepository) UpdateFilePath(_ context.Context, id uuid.UUID, filePath string) error {
	d, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	d.FilePath = filePath
	f.byID[id] = d
	return nil
}

func (f *fakeRepository) UpdateExtraction(_ context.Context, id uuid.UUID, res ExtractionResult) error {
	d, ok := f.byID[id]
	if !ok {
		return errors.New("not found")
	}
	d.StatementTotal = res.StatementTotal
	d.StatementClosingDate = res.StatementClosingDate
	if res.Status != "" {
		d.ExtractionStatus = strPtr(res.Status)
	}
	d.ExtractionDelta = res.Delta
	d.ExtractionNote = res.Note
	switch {
	case res.ClearPendingExtraction:
		d.PendingExtraction = nil
	case res.PendingExtraction != nil:
		d.PendingExtraction = res.PendingExtraction
	}
	f.byID[id] = d
	return nil
}

func strPtr(s string) *string { return &s }

func (f *fakeRepository) FindByContentHash(_ context.Context, hash string) (*Document, error) {
	for _, d := range f.byID {
		if d.ContentHash != nil && *d.ContentHash == hash {
			match := d
			return &match, nil
		}
	}
	return nil, nil
}

func TestService_CreateGetList(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	personID := uuid.New()

	created, err := service.Create(ctx, CreateDocumentRequest{
		PersonID:         &personID,
		OriginalFilename: "passport.pdf",
		FileType:         FileTypePDF,
		Origin:           OriginPersonal,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if created.OriginalFilename != "passport.pdf" {
		t.Fatalf("expected original filename passport.pdf, got %q", created.OriginalFilename)
	}
	if created.HasFile {
		t.Fatalf("a metadata-only Create must not report HasFile")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if got.FileType != FileTypePDF {
		t.Fatalf("expected file type PDF, got %v", got.FileType)
	}

	list, err := service.List(ctx, &personID, nil, nil, "")
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 document, got %d", len(list))
	}
}

func TestService_List_FiltersByQuery(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	desc := "Comprovante de residência"
	inst := "Enel"
	repo.byID[uuid.New()] = Document{ID: uuid.New(), OriginalFilename: "contrato-aluguel.pdf"}
	repo.byID[uuid.New()] = Document{ID: uuid.New(), OriginalFilename: "fatura.pdf", Description: &desc, Institution: &inst}

	byName, err := service.List(ctx, nil, nil, nil, "aluguel")
	if err != nil {
		t.Fatalf("List(name): %v", err)
	}
	if len(byName) != 1 || byName[0].OriginalFilename != "contrato-aluguel.pdf" {
		t.Fatalf("expected the aluguel contract, got %+v", byName)
	}

	byInstitution, err := service.List(ctx, nil, nil, nil, "ENEL")
	if err != nil {
		t.Fatalf("List(institution): %v", err)
	}
	if len(byInstitution) != 1 || byInstitution[0].OriginalFilename != "fatura.pdf" {
		t.Fatalf("expected fatura.pdf by institution (case-insensitive), got %+v", byInstitution)
	}

	none, err := service.List(ctx, nil, nil, nil, "inexistente")
	if err != nil {
		t.Fatalf("List(miss): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no matches, got %d", len(none))
	}
}

func TestService_Update_DoesNotChangeFileMetadata(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	created, err := service.Create(ctx, CreateDocumentRequest{
		OriginalFilename: "original.pdf",
		FileType:         FileTypePDF,
		Origin:           OriginPersonal,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	tags := "important"
	updated, err := service.Update(ctx, created.ID, UpdateDocumentRequest{Tags: &tags})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}
	if updated.Tags == nil || *updated.Tags != "important" {
		t.Fatalf("expected tags to be updated, got %v", updated.Tags)
	}
	// FileType/OriginalFilename/Origin are not part of UpdateDocumentRequest — confirm they
	// survive the update untouched.
	if updated.OriginalFilename != "original.pdf" || updated.FileType != FileTypePDF || updated.Origin != OriginPersonal {
		t.Fatalf("expected file metadata to remain unchanged after Update, got %+v", updated)
	}
}

func TestService_Classify_ChangesOnlyOrigin(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()
	created, err := service.Create(ctx, CreateDocumentRequest{OriginalFilename: "scan.pdf", FileType: FileTypePDF, Origin: OriginOther})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	classified, err := service.Classify(ctx, created.ID, OriginInvoice)
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if classified.Origin != OriginInvoice || classified.OriginalFilename != created.OriginalFilename || classified.HasFile != created.HasFile {
		t.Fatalf("classification changed unrelated metadata: %+v", classified)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	_, err := service.Get(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	_, err := service.Update(ctx, uuid.New(), UpdateDocumentRequest{})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	err := service.Delete(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

// pdfBytes is a minimal valid PDF signature — enough to pass validateSignature without needing a
// real, fully-formed PDF.
var pdfBytes = append([]byte("%PDF-1.4\n"), []byte("...")...)

// fakeDrive is an in-memory DriveStorage used by unit tests — no OAuth, no network.
type fakeDrive struct {
	files   map[string][]byte
	deleted []string
	nextID  int
	err     error // when set, UploadFile fails with it
}

func newFakeDrive() *fakeDrive { return &fakeDrive{files: make(map[string][]byte)} }

func (f *fakeDrive) UploadFile(_ context.Context, _ Origin, _ FileType, _ string, content []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.nextID++
	id := fmt.Sprintf("fake%d", f.nextID)
	f.files[id] = content
	return id, nil
}

func (f *fakeDrive) FetchFile(_ context.Context, path string) ([]byte, error) {
	data, ok := f.files[driveFileID(path)]
	if !ok {
		return nil, errors.New("fake drive: file not found")
	}
	return data, nil
}

func (f *fakeDrive) DeleteFile(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	delete(f.files, driveFileID(path))
	return nil
}

func TestService_Upload_StoresAndPersists(t *testing.T) {
	repo := newFakeRepository()
	drive := newFakeDrive()
	service := NewService(repo, nil, drive, nil, nil, nil, nil)
	ctx := context.Background()

	dto, err := service.Upload(ctx, UploadRequest{
		Content: pdfBytes, ContentType: "application/pdf", OriginalFilename: "fatura.pdf",
		Origin: OriginInvoice,
	})
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if dto.FileType != FileTypePDF {
		t.Fatalf("expected file type PDF, got %v", dto.FileType)
	}
	if dto.OriginalFilename != "fatura.pdf" {
		t.Fatalf("expected original filename fatura.pdf, got %q", dto.OriginalFilename)
	}
	if !dto.HasFile {
		t.Fatalf("an upload must report HasFile")
	}
	// DocumentDTO no longer exposes the raw path — read the bytes back through the service, which
	// is the only supported way in.
	back, err := service.DownloadFile(ctx, dto.ID)
	if err != nil {
		t.Fatalf("expected the file to actually be on Drive, DownloadFile failed: %v", err)
	}
	if string(back.Content) != string(pdfBytes) {
		t.Fatalf("stored content doesn't match uploaded content")
	}
	stored, err := repo.FindByID(ctx, dto.ID)
	if err != nil || stored == nil {
		t.Fatalf("expected a persisted row, FindByID failed: %v", err)
	}
	if !IsGoogleDrivePath(stored.FilePath) {
		t.Fatalf("expected the upload to be stored on Drive, got FilePath=%q", stored.FilePath)
	}
}

func TestService_Upload_RejectsSignatureMismatch(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	_, err := service.Upload(ctx, UploadRequest{
		Content: []byte("not actually a pdf"), ContentType: "application/pdf",
		OriginalFilename: "fake.pdf", Origin: OriginInvoice,
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error, got %v", err)
	}
}

func TestService_Upload_RejectsOversized(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	_, err := service.Upload(ctx, UploadRequest{
		Content: make([]byte, MaxUploadBytes+1), ContentType: "application/pdf",
		OriginalFilename: "huge.pdf", Origin: OriginInvoice,
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation apperr.Error, got %v", err)
	}
}

// TestService_DownloadFile_LocalLegacyFile covers the read path for documents still backed by local
// disk (uploads made before the move to Drive, or rows the ETL hasn't migrated yet): DownloadFile
// must read them from LocalStorage.
func TestService_DownloadFile_LocalLegacyFile(t *testing.T) {
	repo := newFakeRepository()
	storage := NewLocalStorage(t.TempDir(), nil)
	service := NewService(repo, storage, nil, nil, nil, nil, nil)
	ctx := context.Background()

	path, err := storage.Store(ctx, OriginInvoice, FileTypePDF, pdfBytes)
	if err != nil {
		t.Fatalf("seeding local file: %v", err)
	}
	id := uuid.New()
	if err := repo.Create(ctx, Document{
		ID: id, OriginalFilename: "extrato.pdf", FilePath: path,
		FileType: FileTypePDF, Origin: OriginInvoice,
	}); err != nil {
		t.Fatalf("seeding repo failed: %v", err)
	}

	file, err := service.DownloadFile(ctx, id)
	if err != nil {
		t.Fatalf("DownloadFile returned error: %v", err)
	}
	if string(file.Content) != string(pdfBytes) {
		t.Fatalf("downloaded content doesn't match stored content")
	}
	if file.ContentType != "application/pdf" {
		t.Fatalf("expected content type application/pdf, got %q", file.ContentType)
	}
	if file.Filename != "extrato.pdf" {
		t.Fatalf("expected filename extrato.pdf, got %q", file.Filename)
	}
}

func TestService_DownloadFile_NotFound(t *testing.T) {
	repo := newFakeRepository()
	service := NewService(repo, nil, nil, nil, nil, nil, nil)
	ctx := context.Background()

	_, err := service.DownloadFile(ctx, uuid.New())

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound {
		t.Fatalf("expected a not-found apperr.Error, got %v", err)
	}
}

// fakeIntegrationsLookup lets tests exercise DownloadFile's Google-Drive dispatch branch without
// a real OAuth token exchange — GoogleDriveRefreshToken's error return is checked before
// GoogleDriveClient ever builds an oauth2.TokenSource or makes a network call.
type fakeIntegrationsLookup struct {
	err error
}

func (f *fakeIntegrationsLookup) GoogleDriveRefreshToken(_ context.Context) (string, error) {
	return "", f.err
}

func TestService_DownloadFile_GoogleDrivePath_PropagatesCredentialError(t *testing.T) {
	repo := newFakeRepository()
	notConnected := apperr.NotFound("google drive is not connected")
	drive := NewGoogleDriveClient("client-id", "client-secret", &fakeIntegrationsLookup{err: notConnected})
	service := NewService(repo, nil, drive, nil, nil, nil, nil)
	ctx := context.Background()

	id := uuid.New()
	if err := repo.Create(ctx, Document{
		ID: id, OriginalFilename: "old.pdf", FilePath: "googledrive:abc123",
		FileType: FileTypePDF, Origin: OriginInvoice,
	}); err != nil {
		t.Fatalf("seeding repo failed: %v", err)
	}

	_, err := service.DownloadFile(ctx, id)
	if err == nil || !errors.Is(err, notConnected) {
		t.Fatalf("expected the google drive credential error to propagate, got %v", err)
	}
}

func TestService_Delete_RemovesLocalLegacyFile(t *testing.T) {
	repo := newFakeRepository()
	storage := NewLocalStorage(t.TempDir(), nil)
	service := NewService(repo, storage, nil, nil, nil, nil, nil)
	ctx := context.Background()

	path, err := storage.Store(ctx, OriginInvoice, FileTypePDF, pdfBytes)
	if err != nil {
		t.Fatalf("seeding local file: %v", err)
	}
	id := uuid.New()
	if err := repo.Create(ctx, Document{
		ID: id, OriginalFilename: "to-delete.pdf", FilePath: path,
		FileType: FileTypePDF, Origin: OriginInvoice,
	}); err != nil {
		t.Fatalf("seeding repo failed: %v", err)
	}

	if err := service.Delete(ctx, id); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected the underlying local file to be removed, stat err=%v", err)
	}
	if _, err := repo.FindByID(ctx, id); err != nil {
		t.Fatalf("FindByID after Delete errored: %v", err)
	}
}

// TestService_ExtractLabResult_MissingPerson_SurfacesInInbox regresses production 2026-09-16: a
// LAB_RESULT document uploaded with no PersonID used to make extractLabResult return before ever
// calling UpdateExtraction, leaving extraction_status nil forever — the document never appeared
// in the Inbox and the upload looked like it had silently vanished. It must now stage as FAILED
// with a note the user can act on (link a person, then Reextract).
func TestService_ExtractLabResult_MissingPerson_SurfacesInInbox(t *testing.T) {
	repo := newFakeRepository()
	healthService := health.NewService(nil) // never called on this path — PersonID is nil
	service := NewService(repo, nil, nil, nil, nil, nil, healthService)
	ctx := context.Background()

	doc := Document{ID: uuid.New(), OriginalFilename: "laudo.pdf", FileType: FileTypePDF, Origin: OriginLabResult}
	if err := repo.Create(ctx, doc); err != nil {
		t.Fatalf("seeding repo failed: %v", err)
	}

	service.extractLabResult(ctx, doc, "algum texto extraído do exame")

	updated, err := repo.FindByID(ctx, doc.ID)
	if err != nil {
		t.Fatalf("FindByID returned error: %v", err)
	}
	if updated.ExtractionStatus == nil || *updated.ExtractionStatus != ExtractionFailed {
		t.Fatalf("expected ExtractionStatus FAILED, got %v", updated.ExtractionStatus)
	}
	if updated.ExtractionNote == nil || *updated.ExtractionNote == "" {
		t.Fatalf("expected an actionable note explaining the missing person, got %v", updated.ExtractionNote)
	}
}

func TestService_Delete_RemovesGoogleDriveFile(t *testing.T) {
	repo := newFakeRepository()
	drive := newFakeDrive()
	drive.files["abc123"] = []byte("stored")
	service := NewService(repo, nil, drive, nil, nil, nil, nil)
	ctx := context.Background()

	id := uuid.New()
	if err := repo.Create(ctx, Document{
		ID: id, OriginalFilename: "old.pdf", FilePath: "googledrive:abc123",
		FileType: FileTypePDF, Origin: OriginInvoice,
	}); err != nil {
		t.Fatalf("seeding repo failed: %v", err)
	}

	if err := service.Delete(ctx, id); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if len(drive.deleted) != 1 || drive.deleted[0] != "googledrive:abc123" {
		t.Fatalf("expected the Drive file to be deleted, deleted=%v", drive.deleted)
	}
	if _, ok := drive.files["abc123"]; ok {
		t.Fatalf("Drive file still present after Delete")
	}
}

func TestService_Upload_DriveNotConnected(t *testing.T) {
	repo := newFakeRepository()
	drive := newFakeDrive()
	drive.err = apperr.NotFound("google drive is not connected")
	service := NewService(repo, nil, drive, nil, nil, nil, nil)
	ctx := context.Background()

	_, err := service.Upload(ctx, UploadRequest{
		Content: pdfBytes, ContentType: "application/pdf", OriginalFilename: "fatura.pdf",
		Origin: OriginInvoice,
	})

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
		t.Fatalf("expected a validation error asking to connect Drive, got %v", err)
	}
}

func TestService_MigrateLocalFilesToDrive(t *testing.T) {
	repo := newFakeRepository()
	storage := NewLocalStorage(t.TempDir(), nil)
	drive := newFakeDrive()
	service := NewService(repo, storage, drive, nil, nil, nil, nil)
	ctx := context.Background()

	path, err := storage.Store(ctx, OriginInvoice, FileTypePDF, pdfBytes)
	if err != nil {
		t.Fatalf("seeding local file: %v", err)
	}
	local := Document{ID: uuid.New(), OriginalFilename: "local.pdf", FilePath: path, FileType: FileTypePDF, Origin: OriginInvoice}
	onDrive := Document{ID: uuid.New(), OriginalFilename: "drive.pdf", FilePath: "googledrive:xyz", FileType: FileTypePDF, Origin: OriginInvoice}
	metadata := Document{ID: uuid.New(), OriginalFilename: "meta.pdf", FilePath: metadataFilePath, FileType: FileTypePDF, Origin: OriginInvoice}
	for _, d := range []Document{local, onDrive, metadata} {
		if err := repo.Create(ctx, d); err != nil {
			t.Fatalf("seeding repo: %v", err)
		}
	}

	// dry-run must not upload or rewrite anything.
	dry, err := service.MigrateLocalFilesToDrive(ctx, true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if dry.Pending != 1 || dry.Migrated != 0 || dry.Skipped != 2 {
		t.Fatalf("unexpected dry-run result: %+v", dry)
	}
	if stored, _ := repo.FindByID(ctx, local.ID); stored.FilePath != path {
		t.Fatalf("dry-run must not rewrite FilePath, got %q", stored.FilePath)
	}

	res, err := service.MigrateLocalFilesToDrive(ctx, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Migrated != 1 || res.Skipped != 2 || len(res.Failures) != 0 {
		t.Fatalf("unexpected migration result: %+v", res)
	}
	stored, _ := repo.FindByID(ctx, local.ID)
	if !IsGoogleDrivePath(stored.FilePath) {
		t.Fatalf("expected migrated FilePath to be on Drive, got %q", stored.FilePath)
	}
	if string(drive.files[driveFileID(stored.FilePath)]) != string(pdfBytes) {
		t.Fatalf("migrated bytes don't match the original local file")
	}
}
