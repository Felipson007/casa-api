package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestDummyPasswordHash_MatchesRealCost — audit F4. LoginWithPassword runs a bcrypt compare
// against dummyPasswordHash when the account is missing or has no password, so that path costs
// the same as "real account, wrong password". If the dummy hash's cost drifts from the cost real
// password_hash rows use, the equaliser stops equalising and the timing oracle is back.
func TestDummyPasswordHash_MatchesRealCost(t *testing.T) {
	cost, err := bcrypt.Cost(dummyPasswordHash)
	if err != nil {
		t.Fatalf("dummyPasswordHash is not a valid bcrypt hash: %v", err)
	}
	if cost != bcrypt.DefaultCost {
		t.Fatalf("dummyPasswordHash cost = %d, want bcrypt.DefaultCost (%d) — same as GenerateFromPassword uses everywhere", cost, bcrypt.DefaultCost)
	}
	// Sanity: it must not accidentally accept a real-looking password.
	if bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte("password")) == nil {
		t.Fatal("dummyPasswordHash should not verify against a plausible password")
	}
}
