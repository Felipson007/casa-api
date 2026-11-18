package documents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/go-pdf/fpdf"

	"github.com/atlas/atlas-go/internal/pandora"
)

func TestParseAmountPreservesDecimalPoint(t *testing.T) {
	tests := map[string]string{
		"1234.56":     "1234.56",
		"R$ 1.234,56": "1234.56",
		"1.234":       "1234",
		"12.50":       "12.50",
	}
	for input, want := range tests {
		got, ok := parseAmount(input)
		if !ok || got != want {
			t.Errorf("parseAmount(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
}

func TestParseFirstJSONIntoSupportsArrays(t *testing.T) {
	var result struct {
		Items []struct {
			Amount string `json:"amount"`
		} `json:"items"`
	}
	err := parseFirstJSONInto("```json\n{\"items\":[{\"amount\":\"10.90\"}]}\n```", &result)
	if err != nil || len(result.Items) != 1 || result.Items[0].Amount != "10.90" {
		t.Fatalf("parseFirstJSONInto() result=%+v err=%v", result, err)
	}
}

func TestExpenseFingerprintNormalisesEquivalentItems(t *testing.T) {
	date, ok := parseDate("2026-08-24")
	if !ok {
		t.Fatal("expected valid date")
	}
	first := expenseFingerprint("R$ 1.234,56", date, "  Mercado   Central ")
	second := expenseFingerprint("1234.56", date, "mercado central")
	if first != second {
		t.Fatalf("equivalent invoice items produced different fingerprints: %q != %q", first, second)
	}
	if expenseFingerprint("12.5", date, "Taxa") != expenseFingerprint("12.50", date, "taxa") {
		t.Fatal("equivalent decimal scales produced different fingerprints")
	}
}

func TestSafeGoRecoversPanicWithoutCrashingCaller(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	safeGo(context.Background(), func() {
		defer wg.Done()
		panic("boom")
	})
	wg.Wait() // if safeGo didn't recover, this test binary would have already crashed.
}

func TestExtractionFailureNotePrefersRateLimitMessage(t *testing.T) {
	rateLimited := fmt.Errorf("chunk failed: %w", pandora.ErrRateLimited)
	if got := extractionFailureNote(rateLimited, "fallback"); got == "fallback" {
		t.Fatal("expected a rate-limit-specific note, got the fallback")
	}

	other := errors.New("boom")
	if got := extractionFailureNote(other, "fallback"); got != "fallback" {
		t.Fatalf("extractionFailureNote() = %q, want the fallback for a non-rate-limit error", got)
	}

	if got := extractionFailureNote(nil, "fallback"); got != "fallback" {
		t.Fatalf("extractionFailureNote(nil) = %q, want the fallback", got)
	}
}

// TestExtractionFailureNotePrefersModelOverloadedMessage regresses production 2026-09-17: a lab
// result upload hit pandora.ErrModelOverloaded (503) and staged with the generic fallback, which
// wrongly implied the file itself couldn't be read instead of naming the transient AI outage.
func TestExtractionFailureNotePrefersModelOverloadedMessage(t *testing.T) {
	overloaded := fmt.Errorf("structuring failed: %w", pandora.ErrModelOverloaded)
	got := extractionFailureNote(overloaded, "fallback")
	if got == "fallback" {
		t.Fatal("expected a model-overloaded-specific note, got the fallback")
	}
	if got == extractionFailureNote(fmt.Errorf("%w", pandora.ErrRateLimited), "fallback") {
		t.Fatal("model-overloaded note should be distinct from the rate-limit note (different cause)")
	}
}

func TestExtractPDFTextReturnsErrorInsteadOfPanickingOnMalformedInput(t *testing.T) {
	// Not a valid PDF at all — exercises ledongthuc/pdf's error/panic path on malformed input
	// (production saw a panic here: "malformed PDF: reading at offset 18056: unexpected EOF").
	_, err := extractPDFText([]byte("not a pdf"))
	if err == nil {
		t.Fatal("expected an error for malformed PDF content, got nil")
	}
}

// TestExtractPDFTextReadsATextLayer is the positive-path counterpart to the malformed-input test
// above: it confirms extractPDFText actually recovers text from a born-digital PDF (one with a
// real text layer, generated here with the same fpdf library the report generator uses) instead
// of only being exercised against garbage input. documentText relies on this returning non-empty
// text to skip the gateway/vision call entirely for ordinary digital invoices and paystubs — a
// regression here would silently push every PDF through the (slower, quota-limited) multimodal
// path instead of the free local one.
func TestExtractPDFTextReadsATextLayer(t *testing.T) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 14)
	pdf.Cell(40, 10, "TESTE EXTRACAO 123")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatalf("failed to render test PDF: %v", err)
	}

	text, err := extractPDFText(buf.Bytes())
	if err != nil {
		t.Fatalf("extractPDFText() error = %v", err)
	}
	if !strings.Contains(text, "TESTE EXTRACAO 123") {
		t.Fatalf("extractPDFText() = %q, want it to contain the PDF's text layer", text)
	}
}
