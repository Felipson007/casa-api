package personrelations

import (
	"testing"

	"github.com/google/uuid"
)

// Regression tests for two bugs found while comparing the Go rewrite against real production
// data (a real family graph, unlike any synthetic fixture, has the topology needed to surface
// them): (1) gender was never wired into the derivation at all, so every gendered term silently
// defaulted; (2) matching by category *count* instead of the exact sequence collapsed unrelated
// 3-step relationships (e.g. a sibling's spouse) into a 2-step bucket (e.g. a spouse's parent)
// whenever the counts happened to overlap.
func TestDeriveFromOrigin_GenderDisambiguation(t *testing.T) {
	self, mother, father, sister, sisterHusband := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	edges := []Edge{
		{From: mother, To: self, Type: MotherOf},
		{From: father, To: self, Type: FatherOf},
		{From: mother, To: sister, Type: MotherOf},
		{From: father, To: sister, Type: FatherOf},
		{From: sister, To: sisterHusband, Type: SpouseOf},
	}
	genders := map[uuid.UUID]Gender{
		mother: GenderFemale,
		father: GenderMale,
		sister: GenderFemale,
	}

	got := DeriveFromOrigin(self, edges, genders)

	if got[mother].Parentesco != Parent {
		t.Fatalf("mother: expected Parent, got %+v", got[mother])
	}
	if got[sister].Parentesco != Sibling {
		t.Fatalf("sister: expected Sibling, got %+v", got[sister])
	}
	// sisterHusband is reached via SOBE-DESCE-LADO (up to a shared parent, down to the sister,
	// across to her spouse) — a brother/sister-in-law, which has no Kinship enum value in this
	// API (mirroring parentesco-grafo.ts, which also leaves it as a free label). Before the fix,
	// this 3-step path collided with the 2-step "LADO-SOBE" (spouse's parent) bucket purely
	// because both contain exactly one LADO and one SOBE step, mislabeling every in-law reached
	// through a sibling as the target's own parent-in-law.
	rel, ok := got[sisterHusband]
	if !ok {
		t.Fatalf("sisterHusband: expected a derived entry, got none")
	}
	if rel.Parentesco == InLawFather || rel.Parentesco == InLawMother {
		t.Fatalf("sisterHusband: must not collide with the spouse's-parent bucket, got %+v", rel)
	}
	if rel.RotuloLivre != "Cunhado" {
		t.Fatalf("sisterHusband: expected free-label 'Cunhado', got %+v", rel)
	}
}

func TestDeriveFromOrigin_InLawGenderSplit(t *testing.T) {
	self, spouse, spouseMother, spouseFather := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	edges := []Edge{
		{From: self, To: spouse, Type: SpouseOf},
		{From: spouseMother, To: spouse, Type: MotherOf},
		{From: spouseFather, To: spouse, Type: FatherOf},
	}
	genders := map[uuid.UUID]Gender{spouseMother: GenderFemale, spouseFather: GenderMale}

	got := DeriveFromOrigin(self, edges, genders)

	if got[spouseMother].Parentesco != InLawMother {
		t.Fatalf("spouse's mother: expected InLawMother, got %+v", got[spouseMother])
	}
	if got[spouseFather].Parentesco != InLawFather {
		t.Fatalf("spouse's father: expected InLawFather, got %+v", got[spouseFather])
	}
}

func TestDeriveFromOrigin_UnknownGenderDefaultsMale(t *testing.T) {
	self, uncle := uuid.New(), uuid.New()
	sharedParent, uncleParent := uuid.New(), uuid.New()
	edges := []Edge{
		{From: uncleParent, To: sharedParent, Type: FatherOf},
		{From: uncleParent, To: uncle, Type: FatherOf},
		{From: sharedParent, To: self, Type: FatherOf},
	}
	// uncle's gender is deliberately absent from the map — must default to male, matching the
	// ported TS fallback ("senão, masculino por padrão"), not silently omit the entry.
	got := DeriveFromOrigin(self, edges, map[uuid.UUID]Gender{})

	if got[uncle].Parentesco != Uncle {
		t.Fatalf("expected Uncle (default-male fallback), got %+v", got[uncle])
	}
}

func TestDeriveFromOrigin_UnmatchedPathHasNoLabel(t *testing.T) {
	// A great-great-grandparent (SOBE-SOBE-SOBE) has no entry in the 19-case pattern table —
	// must be omitted entirely, not forced into the nearest-shaped bucket.
	self, gp, ggp, gggp := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	edges := []Edge{
		{From: gp, To: self, Type: FatherOf},
		{From: ggp, To: gp, Type: FatherOf},
		{From: gggp, To: ggp, Type: FatherOf},
	}
	got := DeriveFromOrigin(self, edges, nil)

	if _, ok := got[gggp]; ok {
		t.Fatalf("expected no derived entry for an unrecognized path, got %+v", got[gggp])
	}
	if got[ggp].Parentesco != Grandparent {
		t.Fatalf("expected Grandparent for the 2-step path, got %+v", got[ggp])
	}
}

func TestDeriveFromOrigin_Self(t *testing.T) {
	self := uuid.New()
	got := DeriveFromOrigin(self, nil, nil)
	if got[self].Parentesco != Self {
		t.Fatalf("expected Self for the origin, got %+v", got[self])
	}
}
