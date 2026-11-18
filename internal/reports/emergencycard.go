package reports

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/go-pdf/fpdf"
	"github.com/google/uuid"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/people"
	"github.com/atlas/atlas-go/internal/personinfo"
)

// criticalRecordTypes mirrors the frontend's TIPOS_CRITICOS (app/(app)/emergencia/page.tsx) — the
// health record types that matter in an emergency, out of health's much larger catalog (weight,
// lab results, vaccines, ...).
var criticalRecordTypes = map[health.RecordType]bool{
	health.RecordTypeBloodType:        true,
	health.RecordTypeAllergy:          true,
	health.RecordTypeMedicalCondition: true,
	health.RecordTypeMedication:       true,
}

// GenerateEmergencyCardPDF replaces the old window.print()-based "ficha de emergência" (the
// browser's print dialog rendering the live page — fragile print CSS, extra manual "save as PDF"
// step, nothing to hand a paramedic instantly) with a real, synchronously-generated PDF built from
// the exact same data source and TIPOS_CRITICOS/contato-de-emergência rules the on-screen page
// already uses (app/(app)/emergencia/page.tsx), so the two can't drift apart.
func (s *Service) GenerateEmergencyCardPDF(ctx context.Context, personID uuid.UUID) (DownloadedReport, error) {
	person, err := s.people.GetPerson(ctx, personID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: loading person for emergency card: %w", err)
	}

	records, err := s.health.ListRecords(ctx, &personID, nil)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: listing health records for emergency card: %w", err)
	}

	info, err := s.personinfo.List(ctx, personID)
	if err != nil {
		return DownloadedReport{}, fmt.Errorf("reports: listing person information for emergency card: %w", err)
	}

	companyName, _ := companyBranding(s.settings, ctx)
	pdfBytes, err := buildEmergencyCardPDF(companyName, person, records, info)
	if err != nil {
		return DownloadedReport{}, err
	}
	return DownloadedReport{
		Content: pdfBytes, ContentType: "application/pdf",
		Filename: "ficha-emergencia.pdf",
	}, nil
}

// latestByValue mirrors the frontend's porTipo: filter to one record type, drop cancelled/empty
// entries, keep the newest occurrence of each distinct value/notes (a person re-confirming the same
// allergy on every checkup shouldn't repeat it three times), sorted newest first.
func latestByValue(records []health.RecordDTO, recordType health.RecordType) []health.RecordDTO {
	filtered := make([]health.RecordDTO, 0, len(records))
	for _, r := range records {
		if r.Type != recordType || r.Status == health.StatusCancelled {
			continue
		}
		if recordValue(r) == "" {
			continue
		}
		filtered = append(filtered, r)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].RecordDate.Time().Equal(filtered[j].RecordDate.Time()) {
			return filtered[i].RecordDate.Time().After(filtered[j].RecordDate.Time())
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	seen := make(map[string]bool, len(filtered))
	result := make([]health.RecordDTO, 0, len(filtered))
	for _, r := range filtered {
		key := normalizeKey(recordValue(r))
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, r)
	}
	return result
}

func recordValue(r health.RecordDTO) string {
	if r.Amount != nil && strings.TrimSpace(*r.Amount) != "" {
		return strings.TrimSpace(*r.Amount)
	}
	if r.Notes != nil {
		return strings.TrimSpace(*r.Notes)
	}
	return ""
}

// stripDiacritics mirrors the frontend's normalizar(): decompose to NFD, then drop the
// combining-mark runes it leaves behind, so "emergência" and "emergencia" compare equal.
// Falls back to the original string on the (practically never happening) transform error
// rather than losing the value entirely.
func stripDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return result
}

// normalizeKey mirrors the frontend's normalizar(): accent-insensitive, case-insensitive,
// trimmed — used both to compare personinfo keys ("Contato de emergência" vs. the ASCII heuristic
// below) and to dedupe health record values in latestByValue.
func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(stripDiacritics(s)))
}

// mostRecentValue is latestByValue()[0]'s value, or "N/A" — mirrors the frontend's valorMaisRecente.
func mostRecentValue(records []health.RecordDTO, recordType health.RecordType) string {
	list := latestByValue(records, recordType)
	if len(list) == 0 {
		return "N/A"
	}
	return recordValue(list[0])
}

// emergencyContact mirrors the frontend's `contato` lookup: a free-text personinfo entry whose key
// mentions "contato de emergencia"/"telefone de emergencia" — same heuristic, same limitation
// (nothing structured backs this yet), kept identical on purpose so the PDF and the on-screen page
// never show a different answer to "who do I call".
func emergencyContact(info []personinfo.InformationDTO) string {
	for _, item := range info {
		key := normalizeKey(item.Key)
		if strings.Contains(key, "contato de emergencia") || strings.Contains(key, "telefone de emergencia") {
			return item.Value
		}
	}
	return "N/A"
}

func buildEmergencyCardPDF(companyName string, person people.Person, records []health.RecordDTO, info []personinfo.InformationDTO) ([]byte, error) {
	pdf, tr := newReportPDF()
	writeReportHeader(pdf, tr, companyName, "IDENTIFICAÇÃO MÉDICA DE EMERGÊNCIA")
	pdf.Ln(4)

	bloodType := mostRecentValue(records, health.RecordTypeBloodType)
	contact := emergencyContact(info)
	conditions := latestByValue(records, health.RecordTypeMedicalCondition)
	allergies := latestByValue(records, health.RecordTypeAllergy)
	medications := latestByValue(records, health.RecordTypeMedication)

	writeEmergencyIdentityBlock(pdf, tr, person, bloodType, contact, conditions)
	pdf.Ln(4)

	writeEmergencyList(pdf, tr, "Alergias", allergies, "Nenhuma alergia informada.")
	writeEmergencyList(pdf, tr, "Condições médicas", conditions, "Nenhuma condição informada.")
	writeEmergencyList(pdf, tr, "Medicamentos em uso", medications, "Nenhum medicamento informado.")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("reports: rendering emergency card pdf: %w", err)
	}
	return buf.Bytes(), nil
}

// writeEmergencyIdentityBlock draws the name/blood-type/emergency-contact header as a high-contrast
// dark card — mirrors the on-screen page's own red/slate gradient banner (its single most important
// visual cue: a paramedic scanning the page needs blood type and who-to-call in under two seconds,
// not buried in a paragraph).
func writeEmergencyIdentityBlock(pdf *fpdf.Fpdf, tr func(string) string, person people.Person, bloodType, contact string, conditions []health.RecordDTO) {
	const boxHeight = 34.0
	x, y := pdf.GetX(), pdf.GetY()

	pdf.SetFillColor(20, 20, 30)
	pdf.Rect(x, y, pageWidth, boxHeight, "F")
	pdf.SetFillColor(200, 40, 40)
	pdf.Rect(x, y, 3, boxHeight, "F")

	pdf.SetXY(x+8, y+5)
	pdf.SetFont("Arial", "B", 15)
	pdf.SetTextColor(255, 255, 255)
	pdf.CellFormat(pageWidth-55, 7, tr(person.Name), "", 1, "L", false, 0, "")

	pdf.SetX(x + 8)
	pdf.SetFont("Arial", "", 9)
	pdf.SetTextColor(210, 210, 215)
	condSummary := "Nenhuma condição crítica informada"
	if len(conditions) > 0 {
		names := make([]string, 0, min(len(conditions), 4))
		for _, c := range conditions[:min(len(conditions), 4)] {
			names = append(names, recordValue(c))
		}
		condSummary = strings.Join(names, " · ")
	}
	pdf.MultiCell(pageWidth-55, 5, tr(condSummary), "", "L", false)

	pdf.SetXY(x+8, y+boxHeight-9)
	pdf.SetFont("Arial", "B", 9)
	pdf.SetTextColor(150, 220, 180)
	pdf.CellFormat(pageWidth-55, 5, tr("Em caso de emergência: "+contact), "", 1, "L", false, 0, "")

	pdf.SetFillColor(255, 255, 255)
	pdf.Rect(x+pageWidth-45, y+5, 40, boxHeight-10, "F")
	pdf.SetXY(x+pageWidth-45, y+7)
	pdf.SetFont("Arial", "", 7)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(40, 4, tr("TIPO SANGUINEO"), "", 1, "C", false, 0, "")
	pdf.SetX(x + pageWidth - 45)
	pdf.SetFont("Arial", "B", 20)
	pdf.SetTextColor(200, 40, 40)
	pdf.CellFormat(40, 12, tr(bloodType), "", 1, "C", false, 0, "")

	pdf.SetTextColor(0, 0, 0)
	pdf.SetXY(x, y+boxHeight+4)
}

// writeEmergencyList draws one titled list of health records — a compact "label + notes" row per
// item, or a muted "nothing here" line when empty, so the section is never silently absent (an
// empty allergy section that just isn't printed at all could read as "not checked" instead of
// "checked, none found").
func writeEmergencyList(pdf *fpdf.Fpdf, tr func(string) string, title string, records []health.RecordDTO, emptyMessage string) {
	writeSectionTitle(pdf, tr, title, 11, 7)
	if len(records) == 0 {
		pdf.SetFont("Arial", "", 9)
		pdf.SetTextColor(mutedRGB[0], mutedRGB[1], mutedRGB[2])
		pdf.CellFormat(0, 5, tr(emptyMessage), "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.Ln(2)
		return
	}

	for _, r := range records {
		pdf.SetFont("Arial", "B", 9)
		pdf.CellFormat(0, 5, tr(recordValue(r)), "", 1, "L", false, 0, "")
		pdf.SetFont("Arial", "", 8)
		pdf.SetTextColor(mutedRGB[0], mutedRGB[1], mutedRGB[2])
		pdf.CellFormat(0, 4.5, tr("Registrado em "+r.RecordDate.Time().Format("02/01/2006")), "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	}
	pdf.Ln(2)
}
