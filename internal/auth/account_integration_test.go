//go:build integration

package auth_test

import (
	"context"
	"testing"

	"github.com/atlas/atlas-go/internal/auth"
)

func TestUpdateProfile_PersistsStructuredName(t *testing.T) {
	pool := newPool(t)
	store := auth.NewStore(pool)
	ctx := context.Background()
	id := insertUser(t, pool, "MEMBER")

	if err := store.UpdateProfile(ctx, id, "Gustavo", "Araujo"); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	p, err := store.FindProfile(ctx, id)
	if err != nil || p == nil {
		t.Fatalf("FindProfile: profile=%v err=%v", p, err)
	}
	if p.FirstName == nil || *p.FirstName != "Gustavo" {
		t.Fatalf("FirstName = %v", p.FirstName)
	}
	if p.LastName == nil || *p.LastName != "Araujo" {
		t.Fatalf("LastName = %v", p.LastName)
	}
	if p.Name == nil || *p.Name != "Gustavo Araujo" {
		t.Fatalf("denormalized Name = %v", p.Name)
	}
}

func TestChangePassword_AndCheckPassword(t *testing.T) {
	pool := newPool(t)
	store := auth.NewStore(pool)
	ctx := context.Background()
	id := insertUser(t, pool, "MEMBER")

	// Google-only account: no current password required.
	if err := store.ChangePassword(ctx, id, "", "first-password"); err != nil {
		t.Fatalf("initial ChangePassword: %v", err)
	}
	ok, err := store.CheckPassword(ctx, id, "first-password")
	if err != nil || !ok {
		t.Fatalf("CheckPassword correct: ok=%v err=%v", ok, err)
	}
	if ok, _ := store.CheckPassword(ctx, id, "wrong"); ok {
		t.Fatal("wrong password must not check out")
	}
	// Wrong current password must be rejected once a password exists.
	if err := store.ChangePassword(ctx, id, "wrong", "second-password"); err == nil {
		t.Fatal("expected error changing password with wrong current")
	}
	if err := store.ChangePassword(ctx, id, "first-password", "second-password"); err != nil {
		t.Fatalf("ChangePassword with correct current: %v", err)
	}
	if ok, _ := store.CheckPassword(ctx, id, "second-password"); !ok {
		t.Fatal("new password must check out")
	}
}

func TestMFALifecycle(t *testing.T) {
	pool := newPool(t)
	store := auth.NewStore(pool)
	ctx := context.Background()
	id := insertUser(t, pool, "MEMBER")

	encrypted := []byte("sealed-secret")
	if err := store.SetMFASecret(ctx, id, encrypted); err != nil {
		t.Fatalf("SetMFASecret: %v", err)
	}
	got, enabled, err := store.MFASecret(ctx, id)
	if err != nil {
		t.Fatalf("MFASecret: %v", err)
	}
	if enabled {
		t.Fatal("MFA must not be enabled before activation")
	}
	if string(got) != string(encrypted) {
		t.Fatalf("secret = %q", got)
	}

	hash := auth.HashRecoveryCode("abcd-efgh")
	if err := store.ActivateMFA(ctx, id, []string{hash}); err != nil {
		t.Fatalf("ActivateMFA: %v", err)
	}
	if _, enabled, _ := store.MFASecret(ctx, id); !enabled {
		t.Fatal("MFA must be enabled after activation")
	}

	used, err := store.ConsumeRecoveryCode(ctx, id, hash)
	if err != nil || !used {
		t.Fatalf("first ConsumeRecoveryCode: used=%v err=%v", used, err)
	}
	if used, _ := store.ConsumeRecoveryCode(ctx, id, hash); used {
		t.Fatal("recovery code must be single-use")
	}

	if err := store.DisableMFA(ctx, id); err != nil {
		t.Fatalf("DisableMFA: %v", err)
	}
	if secret, enabled, _ := store.MFASecret(ctx, id); enabled || secret != nil {
		t.Fatal("DisableMFA must clear the secret and flag")
	}
}

func TestMFAChallengeLifecycle(t *testing.T) {
	pool := newPool(t)
	store := auth.NewStore(pool)
	ctx := context.Background()
	id := insertUser(t, pool, "MEMBER")

	raw, _, err := store.CreateMFAChallenge(ctx, id)
	if err != nil {
		t.Fatalf("CreateMFAChallenge: %v", err)
	}
	c, err := store.FindMFAChallenge(ctx, raw)
	if err != nil || c == nil {
		t.Fatalf("FindMFAChallenge: c=%v err=%v", c, err)
	}
	if c.AppUserID != id {
		t.Fatalf("challenge user = %s want %s", c.AppUserID, id)
	}
	if err := store.MarkMFAChallengeUsed(ctx, c.ID); err != nil {
		t.Fatalf("MarkMFAChallengeUsed: %v", err)
	}
	if c, _ := store.FindMFAChallenge(ctx, raw); c != nil {
		t.Fatal("used challenge must not resolve")
	}
}
