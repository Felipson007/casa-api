package auth

import (
	"encoding/base64"
	"regexp"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestValidateTOTPRoundTrip(t *testing.T) {
	key, err := newTOTPKey("Atlas", "user@test.local")
	if err != nil {
		t.Fatalf("newTOTPKey: %v", err)
	}
	if key.Secret() == "" {
		t.Fatal("expected a non-empty secret")
	}
	code, err := totp.GenerateCodeCustom(key.Secret(), time.Now().UTC(), totp.ValidateOpts{
		Period: totpPeriod, Skew: totpSkew, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	if !validateTOTP(key.Secret(), code) {
		t.Fatalf("expected code %q to validate", code)
	}
	if validateTOTP(key.Secret(), "abc") {
		t.Fatal("malformed code must not validate")
	}
	if validateTOTP(key.Secret(), "") {
		t.Fatal("empty code must not validate")
	}
}

func TestQRPNGBase64(t *testing.T) {
	key, err := newTOTPKey("Atlas", "user@test.local")
	if err != nil {
		t.Fatalf("newTOTPKey: %v", err)
	}
	encoded, err := qrPNGBase64(key)
	if err != nil {
		t.Fatalf("qrPNGBase64: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("qr is not valid base64: %v", err)
	}
	magic := []byte{0x89, 'P', 'N', 'G'}
	for i, b := range magic {
		if raw[i] != b {
			t.Fatalf("decoded QR is not a PNG (byte %d = %#x)", i, raw[i])
		}
	}
}

var recoveryCodePattern = regexp.MustCompile(`^[a-z2-7]{4}-[a-z2-7]{4}$`)

func TestNewRecoveryCodes(t *testing.T) {
	codes, err := newRecoveryCodes(recoveryCodeCount)
	if err != nil {
		t.Fatalf("newRecoveryCodes: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("expected %d codes, got %d", recoveryCodeCount, len(codes))
	}
	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if !recoveryCodePattern.MatchString(c) {
			t.Fatalf("code %q does not match xxxx-xxxx base32", c)
		}
		if seen[c] {
			t.Fatalf("duplicate recovery code %q", c)
		}
		seen[c] = true
	}
}

func TestNormalizeAndHashRecoveryCode(t *testing.T) {
	if normalizeRecoveryCode("  ABcd-EFgh ") != "abcdefgh" {
		t.Fatalf("normalize failed: %q", normalizeRecoveryCode("  ABcd-EFgh "))
	}
	if HashRecoveryCode("abcd-efgh") != HashRecoveryCode("ABCDEFGH") {
		t.Fatal("hash must be case/dash insensitive")
	}
	if HashRecoveryCode("abcd-efgh") == HashRecoveryCode("abcd-efgi") {
		t.Fatal("different codes must hash differently")
	}
}

func TestSecretAADIsUserBound(t *testing.T) {
	if string(secretAAD("user-a")) == string(secretAAD("user-b")) {
		t.Fatal("AAD must differ per user")
	}
}
