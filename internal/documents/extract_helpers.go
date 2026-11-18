package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/atlas/atlas-go/internal/health"
	"github.com/atlas/atlas-go/internal/pandora"
	"github.com/atlas/atlas-go/internal/platform/civildate"
	"github.com/atlas/atlas-go/internal/platform/logging"
)

// extractionFailureNote picks the note shown to the user when auto-extraction found no usable
// lines. fallback ("envie o arquivo novamente...") is actively misleading when the real cause was
// the AI gateway itself (rate-limited, or the specific model overloaded) — both resolve themselves
// in a minute or two and need no re-upload — so those errors get their own notes instead. A real
// exam upload hit ErrModelOverloaded and staged with the generic fallback, wrongly pointing at the
// file (production, 2026-09-17).
func extractionFailureNote(err error, fallback string) string {
	switch {
	case errors.Is(err, pandora.ErrRateLimited):
		return "O limite de requisições da IA foi atingido. Aguarde 1-2 minutos e use \"Tentar novamente\" — não é necessário reenviar o arquivo."
	case errors.Is(err, pandora.ErrModelOverloaded):
		return "O modelo de IA está temporariamente sobrecarregado (fora do nosso controle). Aguarde alguns instantes e use \"Tentar novamente\" — não é necessário reenviar o arquivo."
	default:
		return fallback
	}
}

var nonNumericAmountPattern = regexp.MustCompile(`[^0-9.]`)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// safeGo runs fn in its own goroutine with a recover(), logging (with stack trace) instead of
// crashing the whole process if fn panics. Belt-and-suspenders alongside extractPDFText's own
// recover in extract.go: this one catches a panic from *any* step of the fire-and-forget
// extraction pipeline (not just PDF parsing), since it runs off the request goroutine where an
// uncaught panic would otherwise take the whole backend down. ctx is only used for logging (via
// logging.FromContext) — pass the caller's original ctx, not one already wrapped for the work
// itself.
func safeGo(ctx context.Context, fn func()) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				logging.FromContext(ctx).Error("documents: recovered panic in background extraction",
					"panic", rec, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// sha256Hex is a document's content identity — see documents.Service.Upload's duplicate check.
func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func expenseFingerprint(amount string, date civildate.Date, description string) string {
	normalisedAmount, ok := parseAmount(amount)
	if !ok {
		normalisedAmount = strings.TrimSpace(amount)
	} else {
		parts := strings.SplitN(normalisedAmount, ".", 2)
		fraction := "00"
		if len(parts) == 2 {
			fraction = (parts[1] + "00")[:2]
		}
		normalisedAmount = parts[0] + "." + fraction
	}
	return strings.Join([]string{
		normalisedAmount,
		date.Time().Format("2006-01-02"),
		strings.ToLower(strings.Join(strings.Fields(description), " ")),
	}, "|")
}

// truncate keeps a string under max runes (cheap, byte-ish cut — fine for prompting).
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// parseAmount normalises a Brazilian currency string to the "1234.56" form the DTOs expect.
func parseAmount(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "r$", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\u00a0", "")
	// Brazilian format: 1.234,56 → 1234.56
	if strings.Contains(s, ",") {
		parts := strings.SplitN(s, ",", 2)
		intPart := strings.ReplaceAll(parts[0], ".", "")
		frac := parts[1]
		// keep at most 2 fraction digits
		if len(frac) > 2 {
			frac = frac[:2]
		}
		s = intPart + "." + frac
	} else if strings.Count(s, ".") > 1 {
		// Several dots are thousands separators except for a possible final decimal separator.
		last := strings.LastIndex(s, ".")
		fractionDigits := len(s) - last - 1
		if fractionDigits == 1 || fractionDigits == 2 {
			s = strings.ReplaceAll(s[:last], ".", "") + s[last:]
		} else {
			s = strings.ReplaceAll(s, ".", "")
		}
	} else if dot := strings.Index(s, "."); dot >= 0 && len(s)-dot-1 == 3 {
		// A single dot followed by three digits is a Brazilian thousands separator (1.234).
		s = strings.ReplaceAll(s, ".", "")
	}
	// strip any non-numeric/non-dot
	s = nonNumericAmountPattern.ReplaceAllString(s, "")
	if s == "" || s == "." {
		return "", false
	}
	return s, true
}

// parseDate parses a YYYY-MM-DD string into civildate.Date. Returns ok=false if unparseable.
func parseDate(s string) (civildate.Date, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return civildate.Date{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return civildate.Date{}, false
	}
	return civildate.Date(t), true
}

// parseAssessmentStatus maps a free-form model reply to one of the health module's three closed
// AssessmentStatus values. Returns nil (not stored) for anything unrecognized, rather than guessing.
func parseAssessmentStatus(s string) *health.AssessmentStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "NORMAL":
		v := health.AssessmentStatusNormal
		return &v
	case "ATTENTION", "ATENCAO", "ATENÇÃO":
		v := health.AssessmentStatusAttention
		return &v
	case "ALTERED", "ALTERADO":
		v := health.AssessmentStatusAltered
		return &v
	default:
		return nil
	}
}

// prose or markdown fences.
func parseFirstJSON(s string) (map[string]string, error) {
	raw, err := firstJSONObject(s)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseFirstJSONInto(s string, target any) error {
	raw, err := firstJSONObject(s)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func firstJSONObject(s string) ([]byte, error) {
	start := strings.Index(s, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON found in reply")
	}
	depth := 0
	inStr := false
	var esc bool
	for i := start; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		switch c {
		case '\\':
			esc = true
		case '"':
			inStr = !inStr
		case '{':
			if !inStr {
				depth++
			}
		case '}':
			if !inStr {
				depth--
				if depth == 0 {
					raw := s[start : i+1]
					raw = strings.TrimSpace(raw)
					raw = strings.TrimPrefix(raw, "```json")
					raw = strings.TrimPrefix(raw, "```")
					raw = strings.TrimSuffix(raw, "```")
					return []byte(raw), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("unterminated JSON in reply")
}

// firstOfMonthUTC normalizes t to the 1st of its month at UTC midnight — the competência
// convention every expense/income line imported from one statement shares, matching how
// internal/expenses/reports.go builds its month buckets (time.Date(y, m, 1, ..., time.UTC)).
func firstOfMonthUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// parseMonthStart reads a loosely-formatted month/date the model returned for a statement's
// closing or due date — "2026-09", "2026-09-15", "09/2026", "15/09/2026", or a full RFC3339
// instant — and returns the 1st of that month at UTC midnight. ok=false when nothing parses.
func parseMonthStart(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{"2006-01-02", "2006-01", "01/2006", "02/01/2006", time.RFC3339, "2006/01/02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return firstOfMonthUTC(t), true
		}
	}
	// Last resort: a leading "YYYY-MM" prefix on a longer string.
	if len(s) >= 7 {
		if t, err := time.Parse("2006-01", s[:7]); err == nil {
			return firstOfMonthUTC(t), true
		}
	}
	return time.Time{}, false
}

// mostCommonMonthStart returns the most frequent month (as its 1st-of-month UTC) among dates,
// each first shifted forward by shiftMonths. Used as the last-resort competência guess for a
// statement with no readable closing/due date: the bulk of a card invoice's purchases fall in the
// month before it closes, so shiftMonths=1 over the line items' original dates lands on the
// closing month. ok=false for an empty slice.
func mostCommonMonthStart(dates []time.Time, shiftMonths int) (time.Time, bool) {
	if len(dates) == 0 {
		return time.Time{}, false
	}
	counts := make(map[time.Time]int, len(dates))
	var best time.Time
	bestN := 0
	for _, d := range dates {
		key := firstOfMonthUTC(d.AddDate(0, shiftMonths, 0))
		counts[key]++
		if n := counts[key]; n > bestN || (n == bestN && key.After(best)) {
			best, bestN = key, n
		}
	}
	return best, true
}
