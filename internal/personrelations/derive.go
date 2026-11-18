// Package personrelations derives indirect family relationships (sibling, grandparent, uncle,
// nephew, in-law, cousin, ...) from the primary edge graph (mother/father/spouse of), by BFS over
// the primary edges. This is a faithful, idiomatic port of the frontend's
// ../pandora-loan-helper-frontend/lib/parentesco-grafo.ts (derivarDeOrigem), brought server-side
// so the backend becomes the source of truth and the frontend stops recomputing the BFS locally
// (docs/architecture-plan.md section 10, item 4).
//
// Why BFS-in-Go and not a SQL view: the rule "match a normalized category sequence against ~19
// path patterns, dependent on the target's gender" is a poor fit for a giant CASE over concatenated
// strings. It is a near-direct translation of the TS logic into a pure Go function operating on the
// already-loaded graph (at most a few hundred nodes — cost negligible).
package personrelations

import "github.com/google/uuid"

// Edge is one primary relationship stored in person_relationships.
// From = the person the relationship emanates from, To = the target,
// Type is one of MOTHER_OF / FATHER_OF / SPOUSE_OF.
type Edge struct {
	From uuid.UUID
	To   uuid.UUID
	Type RelType
}

// RelType is the closed enum of primary edges (matches the CHECK constraint in the migration).
type RelType string

const (
	MotherOf RelType = "MOTHER_OF"
	FatherOf RelType = "FATHER_OF"
	SpouseOf RelType = "SPOUSE_OF"
)

// DerivedRelationship is the result of deriving how "to" relates to "from" (the origin of the BFS).
type DerivedRelationship struct {
	// Parentesco is the normalized kinship term (SIBLING, GRANDPARENT, UNCLE, NEPHEW,
	// INLAW_* , COUSIN, CHILD, PARENT, SPOUSE, SELF) when the path resolves to a known pattern.
	Parentesco Kinship
	// RotuloLivre is a free-text label when no pattern matches but a path exists (rare).
	RotuloLivre string
	// Steps is how many primary edges the derivation traversed.
	Steps int
}

// Kinship is the set of derived terms the API exposes. Kept stable so the frontend can map to a
// label without re-deriving.
type Kinship string

const (
	Self          Kinship = "SELF"
	Parent        Kinship = "PARENT"
	Child         Kinship = "CHILD"
	Spouse        Kinship = "SPOUSE"
	Sibling       Kinship = "SIBLING"
	Grandparent   Kinship = "GRANDPARENT"
	Grandchild    Kinship = "GRANDCHILD"
	Uncle         Kinship = "UNCLE"          // tio (male perspective of the derived target)
	Aunt          Kinship = "AUNT"           // tia
	Nephew        Kinship = "NEPHEW"         // sobrinho
	Niece         Kinship = "NIECE"          // sobrinha
	InLawFather   Kinship = "INLAW_FATHER"   // sogro
	InLawMother   Kinship = "INLAW_MOTHER"   // sogra
	InLawSon      Kinship = "INLAW_SON"      // genro
	InLawDaughter Kinship = "INLAW_DAUGHTER" // nora
	Cousin        Kinship = "COUSIN"
)

// graph is an adjacency structure for the BFS, indexed by person ID.
type graph struct {
	// up: person -> parents + spouses (SOBE / LADO)
	up map[uuid.UUID][]step
	// down: person -> children (DESCE)
	down map[uuid.UUID][]step
}

type step struct {
	to  uuid.UUID
	cat category // SOBE | DESCE | LADO
	typ RelType
}

// category normalizes a raw edge into one of the three BFS move kinds, mirroring the TS
// normalizeStep used by caminhosDe.
type category string

const (
	sobe  category = "SOBE"  // go up to a parent
	desce category = "DESCE" // go down to a child
	lado  category = "LADO"  // go across to a spouse
)

// buildGraph indexes the primary edges into up/down adjacency lists.
func buildGraph(edges []Edge) *graph {
	g := &graph{
		up:   make(map[uuid.UUID][]step),
		down: make(map[uuid.UUID][]step),
	}
	for _, e := range edges {
		switch e.Type {
		case MotherOf, FatherOf:
			// e.From is parent of e.To
			g.up[e.To] = append(g.up[e.To], step{to: e.From, cat: sobe, typ: e.Type})
			g.down[e.From] = append(g.down[e.From], step{to: e.To, cat: desce, typ: e.Type})
		case SpouseOf:
			g.up[e.From] = append(g.up[e.From], step{to: e.To, cat: lado, typ: e.Type})
			g.up[e.To] = append(g.up[e.To], step{to: e.From, cat: lado, typ: e.Type})
		}
	}
	return g
}

// DeriveFromOrigin returns, for every person reachable from originID, the derived relationship to
// the origin — the Go port of derivarDeOrigem. O(n) over the reachable subgraph (BFS), O(1) extra
// per node: one queue pass, no nested DB round-trips.
//
// genders supplies each reachable person's gender (from the explicit people.gender column — the
// new schema's replacement for the old TS code's generoDeId cascade heuristic, since gender is no
// longer inferred, just read). A person missing from the map (gender not recorded) defaults to
// GenderMale, mirroring the TS fallback ("senão, masculino por padrão").
func DeriveFromOrigin(originID uuid.UUID, edges []Edge, genders map[uuid.UUID]Gender) map[uuid.UUID]DerivedRelationship {
	g := buildGraph(edges)
	result := make(map[uuid.UUID]DerivedRelationship)

	// BFS layering: track the category sequence per reached node.
	type nodeState struct {
		id  uuid.UUID
		seq []category // normalized move categories from origin to here
	}
	queue := []nodeState{{id: originID, seq: nil}}
	visited := map[uuid.UUID]bool{originID: true}
	result[originID] = DerivedRelationship{Parentesco: Self, Steps: 0}

	genderOf := func(id uuid.UUID) Gender {
		if gd, ok := genders[id]; ok && gd != GenderUnknown {
			return gd
		}
		return GenderMale
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// expand up (parents + spouses) and down (children) from cur.id
		for _, s := range g.up[cur.id] {
			if visited[s.to] {
				continue
			}
			visited[s.to] = true
			seq := append(append([]category{}, cur.seq...), s.cat)
			queue = append(queue, nodeState{id: s.to, seq: seq})
			if rel, label, ok := termoPorCaminho(seq, genderOf(s.to)); ok {
				result[s.to] = DerivedRelationship{Parentesco: rel, RotuloLivre: label, Steps: len(seq)}
			}
		}
		for _, s := range g.down[cur.id] {
			if visited[s.to] {
				continue
			}
			visited[s.to] = true
			seq := append(append([]category{}, cur.seq...), s.cat)
			queue = append(queue, nodeState{id: s.to, seq: seq})
			if rel, label, ok := termoPorCaminho(seq, genderOf(s.to)); ok {
				result[s.to] = DerivedRelationship{Parentesco: rel, RotuloLivre: label, Steps: len(seq)}
			}
		}
	}

	return result
}

// Gender is the person's explicit gender (people.gender), used to disambiguate gendered kinship
// terms (uncle/aunt, nephew/niece, in-law parent/child).
type Gender string

const (
	GenderMale    Gender = "M"
	GenderFemale  Gender = "F"
	GenderUnknown Gender = ""
)

// pathKey renders a normalized category sequence as the same "SOBE-DESCE-LADO"-shaped string key
// parentesco-grafo.ts matches on (its `normalizar(passos).join('-')`), so the case table below can
// mirror the TS switch one-for-one instead of approximating it by category counts. Matching by
// exact sequence (not just how many of each category appear) matters: e.g. a sibling's spouse
// (SOBE-DESCE-LADO, 3 steps) must never collide with a spouse's parent (LADO-SOBE, 2 steps) just
// because both sequences contain one LADO and one SOBE.
func pathKey(seq []category) string {
	s := ""
	for i, c := range seq {
		if i > 0 {
			s += "-"
		}
		s += string(c)
	}
	return s
}

// termoPorCaminho maps a normalized category sequence + the target's gender to a kinship term,
// mirroring the 19-case pattern table in parentesco-grafo.ts (termoPorCaminho) exactly — an
// unrecognized sequence returns ok=false (no label), matching the TS default case ("melhor nenhum
// rótulo que um errado"). Terms with no Kinship enum equivalent (step-parent, sibling-in-law) are
// returned as a free-text label instead, exactly as the TS rotuloLivre cases do.
func termoPorCaminho(seq []category, gender Gender) (kinship Kinship, label string, ok bool) {
	male := gender == GenderMale
	switch pathKey(seq) {
	case string(sobe):
		return Parent, "", true
	case string(desce):
		return Child, "", true
	case string(lado):
		return Spouse, "", true
	case pathKey([]category{sobe, sobe}):
		return Grandparent, "", true
	case pathKey([]category{desce, desce}):
		return Grandchild, "", true
	case pathKey([]category{sobe, desce}):
		return Sibling, "", true
	case pathKey([]category{sobe, sobe, desce}):
		if male {
			return Uncle, "", true
		}
		return Aunt, "", true
	case pathKey([]category{sobe, desce, desce}):
		if male {
			return Nephew, "", true
		}
		return Niece, "", true
	case pathKey([]category{lado, sobe}):
		if male {
			return InLawFather, "", true
		}
		return InLawMother, "", true
	case pathKey([]category{desce, lado}):
		if male {
			return InLawSon, "", true
		}
		return InLawDaughter, "", true
	case pathKey([]category{sobe, lado}):
		if male {
			return "", "Padrasto", true
		}
		return "", "Madrasta", true
	case pathKey([]category{lado, sobe, desce}), pathKey([]category{sobe, desce, lado}):
		if male {
			return "", "Cunhado", true
		}
		return "", "Cunhada", true
	case pathKey([]category{sobe, sobe, desce, desce}):
		return Cousin, "", true
	}
	return "", "", false
}
