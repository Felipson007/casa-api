package documents

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/platform/apperr"
	"github.com/atlas/atlas-go/internal/platform/httpx"
)

// Handler is the HTTP adapter. It never touches the repository directly.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func parseOptionalUUID(r *http.Request, param string) (*uuid.UUID, error) {
	raw := r.URL.Query().Get(param)
	if raw == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperr.Validation(param+" must be a valid UUID", param)
	}
	return &parsed, nil
}

// List handles GET /api/documents, optionally filtered by ?personId=, ?assetId=, ?petId=, and/or
// ?q= (filename/description/institution/tags).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	personID, err := parseOptionalUUID(r, "personId")
	if err != nil {
		return err
	}
	assetID, err := parseOptionalUUID(r, "assetId")
	if err != nil {
		return err
	}
	petID, err := parseOptionalUUID(r, "petId")
	if err != nil {
		return err
	}

	dtos, err := h.service.List(r.Context(), personID, assetID, petID, strings.TrimSpace(r.URL.Query().Get("q")))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dtos)
	return nil
}

// Get handles GET /api/documents/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	dto, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Create handles POST /api/documents.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.DecodeAndValidate[CreateDocumentRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.Create(r.Context(), req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

// Update handles PUT /api/documents/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	req, err := httpx.DecodeAndValidate[UpdateDocumentRequest](r)
	if err != nil {
		return err
	}

	dto, err := h.service.Update(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Classify handles POST /api/documents/{id}/classify.
func (h *Handler) Classify(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[ClassifyDocumentRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Classify(r.Context(), id, req.Origin)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// Reextract handles POST /api/documents/{id}/extract — re-runs auto-extraction on an already
// uploaded document (recovery for an extraction that failed, e.g. an AI gateway timeout).
func (h *Handler) Reextract(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	dto, err := h.service.Reextract(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusAccepted, dto)
	return nil
}

// Reconcile handles POST /api/documents/{id}/reconcile — the Inbox answering "qual o valor
// correto?" for an invoice/paystub whose auto-extraction could not sum to the printed total.
func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[ReconcileDocumentRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.Reconcile(r.Context(), id, req)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// ConfirmExtraction handles POST /api/documents/{id}/confirm-extraction — the Inbox review dialog
// accepting (possibly edited) staged extraction data, which is what actually persists it into
// expenses/income/health_records for the first time (see Service.ConfirmExtraction).
func (h *Handler) ConfirmExtraction(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}
	req, err := httpx.DecodeAndValidate[ConfirmExtractionRequest](r)
	if err != nil {
		return err
	}
	dto, err := h.service.ConfirmExtraction(r.Context(), id, req.PendingExtraction)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto)
	return nil
}

// maxUploadFormBytes bounds the whole multipart request body (file + form fields), a bit above
// MaxUploadBytes to leave room for the surrounding multipart framing/other fields.
const maxUploadFormBytes = MaxUploadBytes + 1024*1024

// Upload handles POST /api/documents/upload — multipart/form-data with a "file" part plus the
// same metadata fields as CreateDocumentRequest (origin, personId, assetId, petId, documentDate,
// tags), all as plain form values since this isn't JSON.
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadFormBytes)
	if err := r.ParseMultipartForm(maxUploadFormBytes); err != nil {
		return apperr.Validation("the upload is too large or malformed", "file")
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return apperr.Validation("select a file", "file")
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("documents: reading uploaded file: %w", err)
	}

	origin := Origin(r.FormValue("origin"))
	personID, err := parseOptionalFormUUID(r, "personId")
	if err != nil {
		return err
	}
	assetID, err := parseOptionalFormUUID(r, "assetId")
	if err != nil {
		return err
	}
	petID, err := parseOptionalFormUUID(r, "petId")
	if err != nil {
		return err
	}
	var documentDate *time.Time
	if raw := r.FormValue("documentDate"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return apperr.Validation("documentDate must be YYYY-MM-DD", "documentDate")
		}
		documentDate = &parsed
	}
	var tags *string
	if raw := r.FormValue("tags"); raw != "" {
		tags = &raw
	}
	var taxDocKind *TaxDocKind
	if raw := r.FormValue("taxDocKind"); raw != "" {
		kind := TaxDocKind(raw)
		taxDocKind = &kind
	}

	contentType := header.Header.Get("Content-Type")

	dto, err := h.service.Upload(r.Context(), UploadRequest{
		Content: content, ContentType: contentType, OriginalFilename: header.Filename,
		PersonID: personID, AssetID: assetID, PetID: petID, Origin: origin,
		DocumentDate: documentDate, Tags: tags, TaxDocKind: taxDocKind,
	})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, dto)
	return nil
}

func parseOptionalFormUUID(r *http.Request, field string) (*uuid.UUID, error) {
	raw := r.FormValue(field)
	if raw == "" {
		return nil, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperr.Validation(field+" must be a valid UUID", field)
	}
	return &parsed, nil
}

// DownloadFile handles GET /api/documents/{id}/file — serves the document's raw bytes from local
// disk or Google Drive (see Service.DownloadFile).
func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	file, err := h.service.DownloadFile(r.Context(), id)
	if err != nil {
		return err
	}
	filename := strings.ReplaceAll(file.Filename, `"`, `'`)
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Content)
	return nil
}

// Delete handles DELETE /api/documents/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return apperr.Validation("id must be a valid UUID", "id")
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
