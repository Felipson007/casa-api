package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP parameters: RFC 6238's defaults (SHA-1, 6 digits, 30s step) — every authenticator app
// supports them out of the box. Skew 1 tolerates one step of clock drift in either direction.
const (
	totpPeriod = 30
	totpSkew   = 1
)

// mfaChallengeValidity bounds the handoff between "password checked" and "second factor checked".
// Short by design: it is a login-in-progress token, not a session.
const mfaChallengeValidity = 5 * time.Minute

// recoveryCodeCount is how many single-use codes each enrollment mints — the same order every
// major provider hands out.
const recoveryCodeCount = 10

// newTOTPKey mints a fresh TOTP shared secret and the otpauth:// URL / QR image that carry it into
// the user's authenticator app.
func newTOTPKey(issuer, account string) (*otp.Key, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return nil, fmt.Errorf("auth: generating TOTP key: %w", err)
	}
	return key, nil
}

// validateTOTP reports whether code is a currently-valid TOTP for secret, allowing one step of
// clock skew. A malformed code is simply invalid, never an error.
func validateTOTP(secret, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	ok, err := totp.ValidateCustom(code, secret, time.Now().UTC(), totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && ok
}

// qrPNGBase64 renders the enrollment QR as a base64 PNG (no data: prefix) so the frontend can drop
// it straight into an <img src="data:image/png;base64,..."> without a QR library of its own.
func qrPNGBase64(key *otp.Key) (string, error) {
	img, err := key.Image(256, 256)
	if err != nil {
		return "", fmt.Errorf("auth: rendering TOTP QR: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("auth: encoding TOTP QR: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// newRecoveryCodes mints n single-use codes of the form "xxxx-xxxx" (lowercase base32). They are
// high-entropy, so HashRecoveryCode's deterministic SHA-256 is enough for storage — the same
// reasoning session/password-reset tokens use.
func newRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw := make([]byte, 5)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("auth: generating recovery code: %w", err)
		}
		enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		enc = strings.ToLower(enc)
		codes = append(codes, enc[:4]+"-"+enc[4:8])
	}
	return codes, nil
}

// normalizeRecoveryCode tolerates the exact display form ("abcd-efgh"), all-lower/upper, and
// omitted dashes, so a user typing from a printout is not tripped up by formatting.
func normalizeRecoveryCode(code string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	return strings.ReplaceAll(code, "-", "")
}

func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(normalizeRecoveryCode(code)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// hashMFAChallenge hashes the raw challenge token, same deterministic SHA-256 idiom.
func hashMFAChallenge(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func generateMFAChallengeToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generating MFA challenge token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// secretAAD binds an encrypted TOTP secret to the user it belongs to.
func secretAAD(userID string) []byte {
	return []byte("atlas-mfa-secret-v1:" + userID)
}
