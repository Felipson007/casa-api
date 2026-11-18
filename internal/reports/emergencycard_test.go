package reports

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/personinfo"
	"github.com/atlas/atlas-go/internal/platform/civildate"
)

func recordAt(recordType health.RecordType, value string, date string, status health.Status) health.RecordDTO {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return health.RecordDTO{
		ID: uuid.New(), Type: recordType, RecordDate: civildate.Of(d), Status: status,
		Amount: strPtr(value), CreatedAt: d,
	}
}

func TestLatestByValue_DropsCancelledAndEmpty(t *testing.T) {
	records := []health.RecordDTO{
		recordAt(health.RecordTypeAllergy, "Penicilina", "2024-01-01", health.StatusDone),
		recordAt(health.RecordTypeAllergy, "cancelada", "2024-06-01", health.StatusCancelled),
		{ID: uuid.New(), Type: health.RecordTypeAllergy, RecordDate: civildate.Of(time.Now()), Status: health.StatusDone},
	}
	result := latestByValue(records, health.RecordTypeAllergy)
	if len(result) != 1 {
		t.Fatalf("expected exactly 1 record (cancelled and empty excluded), got %d: %+v", len(result), result)
	}
	if recordValue(result[0]) != "Penicilina" {
		t.Fatalf("expected Penicilina, got %q", recordValue(result[0]))
	}
}

func TestLatestByValue_DeduplicatesKeepingNewest(t *testing.T) {
	records := []health.RecordDTO{
		recordAt(health.RecordTypeAllergy, "Penicilina", "2023-01-01", health.StatusDone),
		recordAt(health.RecordTypeAllergy, "penicilina", "2024-06-01", health.StatusDone), // same allergy, re-confirmed later, different case
		recordAt(health.RecordTypeAllergy, "Amendoim", "2024-01-01", health.StatusDone),
	}
	result := latestByValue(records, health.RecordTypeAllergy)
	if len(result) != 2 {
		t.Fatalf("expected 2 distinct allergies (Penicilina deduped), got %d: %+v", len(result), result)
	}
	// Newest occurrence of the deduped value must be the one kept.
	found := false
	for _, r := range result {
		if recordValue(r) == "penicilina" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the newest (2024-06-01) occurrence of the deduped value to survive")
	}
}

func TestLatestByValue_IgnoresOtherRecordTypes(t *testing.T) {
	records := []health.RecordDTO{
		recordAt(health.RecordTypeAllergy, "Penicilina", "2024-01-01", health.StatusDone),
		recordAt(health.RecordTypeMedication, "Losartana", "2024-01-01", health.StatusDone),
	}
	result := latestByValue(records, health.RecordTypeAllergy)
	if len(result) != 1 {
		t.Fatalf("expected only the ALLERGY record, got %d: %+v", len(result), result)
	}
}

func TestMostRecentValue_ReturnsNAWhenEmpty(t *testing.T) {
	if got := mostRecentValue(nil, health.RecordTypeBloodType); got != "N/A" {
		t.Fatalf("expected N/A for no records, got %q", got)
	}
}

func TestMostRecentValue_PicksNewestByDate(t *testing.T) {
	records := []health.RecordDTO{
		recordAt(health.RecordTypeBloodType, "O+", "2020-01-01", health.StatusDone),
		recordAt(health.RecordTypeBloodType, "A+", "2024-01-01", health.StatusDone),
	}
	if got := mostRecentValue(records, health.RecordTypeBloodType); got != "A+" {
		t.Fatalf("expected the most recent blood type (A+), got %q", got)
	}
}

func TestEmergencyContact_FindsByKeyHeuristic(t *testing.T) {
	info := []personinfo.InformationDTO{
		{ID: uuid.New(), Key: "Profissão", Value: "Engenheiro"},
		{ID: uuid.New(), Key: "Contato de emergência", Value: "(11) 99999-0000"},
	}
	if got := emergencyContact(info); got != "(11) 99999-0000" {
		t.Fatalf("expected the emergency contact value, got %q", got)
	}
}

func TestEmergencyContact_NAWhenAbsent(t *testing.T) {
	info := []personinfo.InformationDTO{{ID: uuid.New(), Key: "Profissão", Value: "Engenheiro"}}
	if got := emergencyContact(info); got != "N/A" {
		t.Fatalf("expected N/A when no emergency-contact key exists, got %q", got)
	}
}
