package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atlas/atlas-go/internal/platform/apperr"
)

// Synthetic values only — nothing here is a real Pluggy credential.
const (
	testClientID     = "0123abcd-0000-4000-8000-000000000001"
	testClientSecret = "test-secret-not-real"
	testItemA        = "11111111-1111-4111-8111-111111111111"
	testItemB        = "22222222-2222-4222-8222-222222222222"
)

func TestConnectPluggy_EncryptsAndNeverReturnsTheSecret(t *testing.T) {
	setEncryptionKey(t)
	repo := newFakeRepository()
	svc := NewService(repo, nil)
	ctx := context.Background()

	dto, err := svc.ConnectPluggy(ctx, ConnectPluggyRequest{ClientID: testClientID, ClientSecret: testClientSecret, ItemIDs: []string{testItemA}})
	if err != nil {
		t.Fatalf("ConnectPluggy: %v", err)
	}
	if dto.Type != TypePluggy {
		t.Errorf("type = %s", dto.Type)
	}
	// The DTO shows a masked client id only: never the secret, never the full id.
	raw, _ := json.Marshal(dto)
	if strings.Contains(string(raw), testClientSecret) || strings.Contains(string(raw), testClientID) {
		t.Errorf("DTO leaks a credential: %s", raw)
	}
	if dto.ConnectedAccount == nil || *dto.ConnectedAccount != "0123…" {
		t.Errorf("connectedAccount = %v, want masked '0123…'", dto.ConnectedAccount)
	}
	// At rest it is ciphertext, not JSON with the secret in it.
	stored := repo.external[TypePluggy]
	if bytes.Contains(stored.RefreshTokenEncrypted, []byte(testClientSecret)) {
		t.Error("secret stored in the clear")
	}

	creds, ok, err := svc.PluggyCredentials(ctx)
	if err != nil || !ok {
		t.Fatalf("PluggyCredentials: ok=%v err=%v", ok, err)
	}
	if creds.ClientID != testClientID || creds.ClientSecret != testClientSecret || len(creds.ItemIDs) != 1 || creds.ItemIDs[0] != testItemA {
		t.Errorf("round trip lost data: %+v", creds)
	}
}

func TestConnectPluggy_ReconnectWithBlankSecretKeepsStoredOne(t *testing.T) {
	setEncryptionKey(t)
	svc := NewService(newFakeRepository(), nil)
	ctx := context.Background()
	if _, err := svc.ConnectPluggy(ctx, ConnectPluggyRequest{ClientID: testClientID, ClientSecret: testClientSecret, ItemIDs: []string{testItemA}}); err != nil {
		t.Fatal(err)
	}
	// Only the item list changes; the credentials are left blank on purpose.
	if _, err := svc.ConnectPluggy(ctx, ConnectPluggyRequest{ItemIDs: []string{testItemA, testItemB}}); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	creds, _, _ := svc.PluggyCredentials(ctx)
	if creds.ClientSecret != testClientSecret || creds.ClientID != testClientID {
		t.Errorf("blank credentials must keep the stored ones, got %+v", creds)
	}
	if len(creds.ItemIDs) != 2 {
		t.Errorf("item ids = %v, want both", creds.ItemIDs)
	}
}

func TestConnectPluggy_Validation(t *testing.T) {
	setEncryptionKey(t)
	cases := []struct {
		name string
		req  ConnectPluggyRequest
	}{
		{"first connect needs credentials", ConnectPluggyRequest{ItemIDs: []string{testItemA}}},
		{"needs an item", ConnectPluggyRequest{ClientID: testClientID, ClientSecret: testClientSecret}},
		{"item must be a uuid", ConnectPluggyRequest{ClientID: testClientID, ClientSecret: testClientSecret, ItemIDs: []string{"not-a-uuid"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewService(newFakeRepository(), nil).ConnectPluggy(context.Background(), c.req)
			var appErr *apperr.Error
			if !errors.As(err, &appErr) || appErr.Kind != apperr.KindValidation {
				t.Fatalf("want a validation error, got %v", err)
			}
		})
	}
}

func TestConnectPluggy_AcceptsAPastedList(t *testing.T) {
	setEncryptionKey(t)
	svc := NewService(newFakeRepository(), nil)
	ctx := context.Background()
	// Pasted as one blob (newline/comma separated) with a duplicate: cleaned and de-duplicated.
	if _, err := svc.ConnectPluggy(ctx, ConnectPluggyRequest{
		ClientID: testClientID, ClientSecret: testClientSecret,
		ItemIDs: []string{testItemA + "\n" + testItemB + ", " + testItemA},
	}); err != nil {
		t.Fatal(err)
	}
	creds, _, _ := svc.PluggyCredentials(ctx)
	if len(creds.ItemIDs) != 2 || creds.ItemIDs[0] != testItemA || creds.ItemIDs[1] != testItemB {
		t.Errorf("item ids = %v", creds.ItemIDs)
	}
}

func TestPluggyCredentials_NotConnected(t *testing.T) {
	setEncryptionKey(t)
	_, ok, err := NewService(newFakeRepository(), nil).PluggyCredentials(context.Background())
	if err != nil || ok {
		t.Errorf("ok=%v err=%v, want not connected and no error", ok, err)
	}
}
