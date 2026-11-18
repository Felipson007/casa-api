package documents

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/money"
)

// TestItauStatementRealFixtures runs the reader over real statements kept OUTSIDE the repo (they
// carry the holder's CPF and address): ATLAS_ITAU_FIXTURES=<dir with *.pdf>. Each must reconcile
// with its printed total — parseItauStatement already enforces that, so a nil error is the check.
func TestItauStatementRealFixtures(t *testing.T) {
	dir := os.Getenv("ATLAS_ITAU_FIXTURES")
	if dir == "" {
		t.Skip("ATLAS_ITAU_FIXTURES not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.pdf"))
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no PDFs in %s", dir)
	}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		st, err := parseItauStatement(content)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
			continue
		}
		uber := money.Zero
		for _, it := range st.items {
			if strings.Contains(strings.ToLower(it.description), "uber") && !strings.Contains(strings.ToLower(it.description), "one membership") {
				uber, _ = money.Add(uber, it.amount)
			}
		}
		t.Logf("%s: closing %s due %s total %s lines %d uber %s", filepath.Base(f),
			st.closingDate.Format("2006-01-02"), st.dueDate.Format("2006-01-02"), st.total, len(st.items), uber)
	}
}
