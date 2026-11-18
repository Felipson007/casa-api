package documents

import (
	"encoding/json"
	"log"

	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func toDTO(d Document) DocumentDTO {
	var pending *PendingExtraction
	if d.PendingExtraction != nil {
		pending = &PendingExtraction{}
		if err := json.Unmarshal([]byte(*d.PendingExtraction), pending); err != nil {
			// Shouldn't happen (this package is the only writer of the column) — degrade to
			// omitting it rather than failing the whole document read over one bad row.
			log.Printf("documents: decoding pending_extraction for %s: %v", d.ID, err)
			pending = nil
		}
	}
	return DocumentDTO{
		ID:               d.ID,
		PersonID:         d.PersonID,
		OriginalFilename: d.OriginalFilename,
		HasFile:          d.FilePath != "" && !IsMetadataOnlyPath(d.FilePath),
		FileType:         d.FileType,
		Origin:           d.Origin,
		DocumentDate:     civildate.OfPtr(d.DocumentDate),
		Tags:             d.Tags,
		AssetID:          d.AssetID,
		PetID:            d.PetID,
		Description:      d.Description,
		Institution:      d.Institution,
		Amount:           d.Amount,
		ExpirationDate:   civildate.OfPtr(d.ExpirationDate),
		ExternalURL:      d.ExternalURL,
		Notes:            d.Notes,
		CreatedAt:        d.CreatedAt,

		StatementTotal:       d.StatementTotal,
		StatementClosingDate: civildate.OfPtr(d.StatementClosingDate),
		ExtractionStatus:     d.ExtractionStatus,
		ExtractionDelta:      d.ExtractionDelta,
		ExtractionNote:       d.ExtractionNote,
		PendingExtraction:    pending,

		TaxDocKind: d.TaxDocKind,
	}
}

func toDTOs(ds []Document) []DocumentDTO {
	dtos := make([]DocumentDTO, len(ds))
	for i, d := range ds {
		dtos[i] = toDTO(d)
	}
	return dtos
}
