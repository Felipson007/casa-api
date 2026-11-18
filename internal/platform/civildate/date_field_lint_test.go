package civildate_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestNoDateOnlyFieldsTypedTimeTime is a standing regression guard, not a one-off fix: the bug it
// catches (a request DTO field meant to hold a bare "YYYY-MM-DD" typed time.Time instead of
// civildate.Date) has been reintroduced independently in health, pets, expenses, income, assets,
// documents, and people — each time only caught after a real user hit a 400 "the request body is
// missing or malformed" trying to save a form. See civildate.Date's package doc for exactly why
// time.Time breaks this: it round-trips through JSON as a full RFC3339 instant
// ("2026-08-05T00:00:00Z"), which the client sends as a bare date string that fails to unmarshal
// at all.
//
// Rather than rely on every future module author remembering this by convention, this statically
// parses every internal/**/*.go file (excluding _test.go and postgres/ adapters, which only ever
// see already-typed Go values, never raw client JSON) and fails if any exported "...Request"
// struct has a field whose name looks date-shaped (contains "date", case-insensitive — covers
// ExpenseDate, BirthDate, AcquisitionDate, DocumentDate, etc.) but is typed time.Time or
// *time.Time instead of civildate.Date/*civildate.Date. Domain structs (not ending in "Request")
// are deliberately exempt — they're never JSON-unmarshaled directly from client input, and several
// legitimately keep time.Time internally, converting at the DTO boundary (see e.g.
// expenses.Service's originalDateTime-style helpers).
// exemptions lists the only known-legitimate cases of a "...Request" struct with a date-shaped
// field still typed time.Time — each entry is "package/file.go:StructName.FieldName" and must
// carry its own reasoning here, not just be silently skipped.
var exemptions = map[string]bool{
	// documents/service.go: UploadRequest.DocumentDate — populated by hand-rolled multipart form
	// parsing (see documents/handler.go), never JSON-unmarshaled, so time.Time never sees a raw
	// "YYYY-MM-DD" string the way encoding/json would — the handler already parses that format
	// itself with its own validation.
	"documents/service.go:UploadRequest.DocumentDate": true,
}

func TestNoDateOnlyFieldsTypedTimeTime(t *testing.T) {
	root := repoInternalDir(t)

	var violations []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// postgres/ adapter packages work with already-decoded Go values (never unmarshal raw
			// client JSON directly), and migrations aren't Go source — skip both.
			base := filepath.Base(path)
			if base == "postgres" || base == "migrations" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if !strings.HasSuffix(typeSpec.Name.Name, "Request") {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok || structType.Fields == nil {
					continue
				}
				for _, field := range structType.Fields.List {
					if !isTimeTimeType(field.Type) {
						continue
					}
					for _, name := range field.Names {
						if !strings.Contains(strings.ToLower(name.Name), "date") {
							continue
						}
						rel, _ := filepath.Rel(root, path)
						key := filepath.ToSlash(rel) + ":" + typeSpec.Name.Name + "." + name.Name
						if exemptions[key] {
							continue
						}
						violations = append(violations, key)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/ for date-field lint: %v", err)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("found %d date-named field(s) on a *Request struct still typed time.Time instead of "+
			"civildate.Date — a bare \"YYYY-MM-DD\" from the client will fail to unmarshal (see this "+
			"test's doc comment):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

// isTimeTimeType reports whether expr is "time.Time" or "*time.Time".
func isTimeTimeType(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkgIdent.Name == "time" && sel.Sel.Name == "Time"
}

// repoInternalDir locates this module's internal/ directory relative to this test file's own
// path, so the test works regardless of the working directory `go test` is invoked from.
func repoInternalDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's path via runtime.Caller")
	}
	// this file is internal/platform/civildate/date_field_lint_test.go — internal/ is 2 levels up.
	dir := filepath.Dir(thisFile)
	internalDir := filepath.Clean(filepath.Join(dir, "..", ".."))
	if filepath.Base(internalDir) != "internal" {
		t.Fatalf("expected to resolve to an 'internal' directory, got %q", internalDir)
	}
	return internalDir
}
