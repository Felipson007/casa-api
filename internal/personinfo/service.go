package personinfo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/documents"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// isNotFound reports whether err is an apperr.Error of KindNotFound — used to turn a dependency
// service's own 404 into this package's own apperr.NotFound with a message naming what wasn't
// found, rather than leaking the other module's wording.
func isNotFound(err error) bool {
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}

// EqualizeUpdate is one row's planned change within an Equalize batch — Snapshot is nil when the
// row's Value already equals the target (mirrors Java's equalizar: skip entirely, no history, no
// re-save, for any item that already holds the new value).
type EqualizeUpdate struct {
	ID       uuid.UUID
	NewValue string
	Snapshot *HistoryEntry // non-nil only when a snapshot of the OLD value must be written first
}

// Repository is defined here, at the point of consumption, per Go convention — see
// internal/categories/service.go for the same idiom.
type Repository interface {
	List(ctx context.Context, personID uuid.UUID) ([]Information, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Information, error)
	Create(ctx context.Context, i Information) error
	// CreateBatch inserts every item in one transaction — mirrors Java's criarEmLote (saveAll).
	CreateBatch(ctx context.Context, items []Information) error
	// Update persists i. When snapshot is non-nil, it's inserted into audit_logs (table_name
	// "person_information") in the SAME transaction as the update — mirrors finance.Repository.
	// Update's atomicity, and Java's single @Transactional atualizar.
	Update(ctx context.Context, i Information, snapshot *HistoryEntry) error
	Delete(ctx context.Context, id uuid.UUID) error
	ListHistory(ctx context.Context, informationID uuid.UUID) ([]HistoryEntry, error)
	// Equalize applies every planned update in one transaction — mirrors Java's equalizar being a
	// single @Transactional method covering the whole informacaoIds loop, not N separate ones.
	Equalize(ctx context.Context, updates []EqualizeUpdate) error
}

// Service holds the business rules. people/documents are the concrete sibling services (same
// composition pattern as internal/dashboard.Service) — Create/CreateBatch only ever call their
// existing Get methods to check existence, translating a NotFound into this package's own.
type Service struct {
	repo      Repository
	people    *people.Service
	documents *documents.Service
}

func NewService(repo Repository, peopleSvc *people.Service, documentsSvc *documents.Service) *Service {
	return &Service{repo: repo, people: peopleSvc, documents: documentsSvc}
}

// List returns a person's information facts, ordered by category then key — mirrors Java's
// findAllByPessoaIdOrderByCategoriaAscChaveAsc.
func (s *Service) List(ctx context.Context, personID uuid.UUID) ([]InformationDTO, error) {
	list, err := s.repo.List(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("personinfo: listing: %w", err)
	}
	return toDTOs(list), nil
}

// Get returns a single information fact, or apperr.NotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (InformationDTO, error) {
	i, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return InformationDTO{}, fmt.Errorf("personinfo: finding: %w", err)
	}
	if i == nil {
		return InformationDTO{}, apperr.NotFound("information not found")
	}
	return toDTO(*i), nil
}

// resolveOrigin looks up DocumentID (if given) and returns (origin, error) — OriginImport if it
// resolved to a real document (mirrors Java's origem being EXTRAIDO, which the ETL already maps to
// IMPORT — see Origin's doc comment), OriginManual otherwise. 404s if a DocumentID was given but
// doesn't exist — mirrors Java's criar/criarEmLote exactly.
func (s *Service) resolveOrigin(ctx context.Context, documentID *uuid.UUID) (Origin, error) {
	if documentID == nil {
		return OriginManual, nil
	}
	if _, err := s.documents.Get(ctx, *documentID); err != nil {
		if isNotFound(err) {
			return "", apperr.NotFound("document not found")
		}
		return "", fmt.Errorf("personinfo: looking up document: %w", err)
	}
	return OriginImport, nil
}

// checkPersonExists 404s (as this package's own apperr.NotFound) if personID doesn't resolve —
// mirrors Java's pessoaRepository.findOneById(...).orElseThrow in criar/criarEmLote.
func (s *Service) checkPersonExists(ctx context.Context, personID uuid.UUID) error {
	if _, err := s.people.GetPerson(ctx, personID); err != nil {
		if isNotFound(err) {
			return apperr.NotFound("person not found")
		}
		return fmt.Errorf("personinfo: looking up person: %w", err)
	}
	return nil
}

// Create persists a new information fact — mirrors Java's criar.
func (s *Service) Create(ctx context.Context, req CreateInformationRequest) (InformationDTO, error) {
	if err := s.checkPersonExists(ctx, req.PersonID); err != nil {
		return InformationDTO{}, err
	}
	origin, err := s.resolveOrigin(ctx, req.DocumentID)
	if err != nil {
		return InformationDTO{}, err
	}

	i := Information{
		ID: uuid.New(), PersonID: req.PersonID, Category: req.Category, Key: req.Key, Value: req.Value,
		DocumentID: req.DocumentID, Origin: origin, DocumentType: req.DocumentType,
	}
	if err := s.repo.Create(ctx, i); err != nil {
		return InformationDTO{}, fmt.Errorf("personinfo: creating: %w", err)
	}
	// Re-read rather than mapping `i` directly: CreatedAt is a DB-generated DEFAULT now() — same
	// class of bug found systemically across this codebase (Hermes's HTTP sweep, msg 83 F-3).
	persisted, err := s.repo.FindByID(ctx, i.ID)
	if err != nil {
		return InformationDTO{}, fmt.Errorf("personinfo: re-reading after create: %w", err)
	}
	return toDTO(*persisted), nil
}

// CreateBatch persists every item against one shared person+document lookup — mirrors Java's
// criarEmLote: the post-upload extraction review screen confirming several facts from the same
// source document at once, replacing N calls to Create.
func (s *Service) CreateBatch(ctx context.Context, req CreateInformationBatchRequest) ([]InformationDTO, error) {
	if err := s.checkPersonExists(ctx, req.PersonID); err != nil {
		return nil, err
	}
	origin, err := s.resolveOrigin(ctx, req.DocumentID)
	if err != nil {
		return nil, err
	}

	items := make([]Information, len(req.Items))
	for idx, it := range req.Items {
		items[idx] = Information{
			ID: uuid.New(), PersonID: req.PersonID, Category: it.Category, Key: it.Key, Value: it.Value,
			DocumentID: req.DocumentID, Origin: origin, DocumentType: req.DocumentType,
		}
	}
	if err := s.repo.CreateBatch(ctx, items); err != nil {
		return nil, fmt.Errorf("personinfo: creating batch: %w", err)
	}

	result := make([]InformationDTO, len(items))
	for idx, it := range items {
		persisted, err := s.repo.FindByID(ctx, it.ID)
		if err != nil {
			return nil, fmt.Errorf("personinfo: re-reading batch item after create: %w", err)
		}
		result[idx] = toDTO(*persisted)
	}
	return result, nil
}

// Update replaces Category/Key/Value/DocumentType — mirrors Java's atualizar exactly: a history
// snapshot of the CURRENT (pre-update) values is written first, but ONLY if something actually
// changed (comparing all four fields) — a no-op save creates no history row, avoiding noise.
// DocumentID/Origin/PersonID are never touched here (Java's UpdatePessoaInformacaoRequest doesn't
// carry them either — they stay historical facts of how the row was originally created).
func (s *Service) Update(ctx context.Context, id uuid.UUID, req UpdateInformationRequest) (InformationDTO, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return InformationDTO{}, fmt.Errorf("personinfo: finding: %w", err)
	}
	if existing == nil {
		return InformationDTO{}, apperr.NotFound("information not found")
	}

	changed := existing.Category != req.Category || existing.Key != req.Key || existing.Value != req.Value ||
		!stringPtrEqual(existing.DocumentType, req.DocumentType)

	var snapshot *HistoryEntry
	if changed {
		snapshot = &HistoryEntry{
			ID: uuid.New(), Category: existing.Category, Key: existing.Key, Value: existing.Value,
			DocumentType: existing.DocumentType,
		}
	}

	updated := Information{
		ID: id, PersonID: existing.PersonID, Category: req.Category, Key: req.Key, Value: req.Value,
		DocumentID: existing.DocumentID, Origin: existing.Origin, DocumentType: req.DocumentType,
		CreatedAt: existing.CreatedAt,
	}
	if err := s.repo.Update(ctx, updated, snapshot); err != nil {
		return InformationDTO{}, fmt.Errorf("personinfo: updating: %w", err)
	}
	return toDTO(updated), nil
}

// Delete removes an information fact outright — its history rows cascade via
// audit_logs having no FK back to person_information (see postgres.Repository.Delete's doc
// comment for why history is retained instead), mirrors Java's remover.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("personinfo: finding: %w", err)
	}
	if existing == nil {
		return apperr.NotFound("information not found")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("personinfo: deleting: %w", err)
	}
	return nil
}

// ListHistory returns an information fact's edit history, most recent first — mirrors Java's
// listarHistorico, including that it does NOT itself verify the parent still exists (matches the
// old behavior exactly: a bogus id just returns an empty list, not a 404).
func (s *Service) ListHistory(ctx context.Context, informationID uuid.UUID) ([]HistoryEntryDTO, error) {
	list, err := s.repo.ListHistory(ctx, informationID)
	if err != nil {
		return nil, fmt.Errorf("personinfo: listing history: %w", err)
	}
	return toHistoryDTOs(list), nil
}

// Equalize applies one chosen-correct Value to every listed row — mirrors Java's equalizar: each
// id is checked independently (404 if any doesn't exist), skipped entirely (no history, no
// re-save) if its Value already equals the target, otherwise snapshotted then updated — all within
// one transaction across the whole batch.
func (s *Service) Equalize(ctx context.Context, req EqualizeInformationRequest) error {
	updates := make([]EqualizeUpdate, 0, len(req.InformationIDs))
	for _, id := range req.InformationIDs {
		existing, err := s.repo.FindByID(ctx, id)
		if err != nil {
			return fmt.Errorf("personinfo: finding %s for equalize: %w", id, err)
		}
		if existing == nil {
			return apperr.NotFound(fmt.Sprintf("information %s not found", id))
		}
		if existing.Value == req.Value {
			continue // already correct — no history, no re-save, matches Java exactly
		}
		updates = append(updates, EqualizeUpdate{
			ID: id, NewValue: req.Value,
			Snapshot: &HistoryEntry{
				ID: uuid.New(), Category: existing.Category, Key: existing.Key, Value: existing.Value,
				DocumentType: existing.DocumentType,
			},
		})
	}
	if len(updates) == 0 {
		return nil
	}
	if err := s.repo.Equalize(ctx, updates); err != nil {
		return fmt.Errorf("personinfo: equalizing: %w", err)
	}
	return nil
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
